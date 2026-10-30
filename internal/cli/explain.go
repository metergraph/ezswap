package cli

// The estimate, the run's outcome and the report's notes, in plain words
// for the terminal.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/run"
	"github.com/metergraph/replay/internal/tui"
	"github.com/metergraph/replay/internal/types"
)

// reasonOrder is the order the TypeScript declared the reasons in, which
// breaks ties between equal counts.
var reasonOrder = []types.NotReplayableReason{
	types.ContentUnavailable,
	types.ContentTruncated,
	types.ToolHistory,
	types.UnsupportedContent,
	types.NoMessages,
	types.LastTurnNotUser,
	types.InputTooLarge,
	types.ToolDefinitionsNotOffered,
	types.WebSearchNotReplayed,
	types.UnparseableRequest,
}

var reasons = map[types.NotReplayableReason]string{
	types.ContentUnavailable:        "the trace does not carry the request",
	types.ContentTruncated:          "the captured content was cut short",
	types.ToolHistory:               "the conversation includes earlier tool calls and results",
	types.UnsupportedContent:        "the input has images, audio or files",
	types.NoMessages:                "the request has no messages",
	types.LastTurnNotUser:           "the last turn is not from the user",
	types.InputTooLarge:             "the input is too large to replay",
	types.ToolDefinitionsNotOffered: "production forced a tool choice the replay cannot reproduce",
	types.WebSearchNotReplayed:      "production searched the web (pass --allow-no-search to replay without search)",
	types.UnparseableRequest:        "the request could not be read",
}

// usd is dollars: four places under a cent, two otherwise.
func usd(value float64) string { return tui.USD(value) }

func plural(n int, one string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + one + "s"
}

type explainOptions struct {
	// Where to send a missing price, shown with unpriced models.
	pricingHelpURL string
	allowUnpriced  bool
}

func explainEstimate(plan *run.RunPlan, o explainOptions) string {
	var lines []string
	estimate, settings := plan.Estimate, plan.Settings
	sampled := len(plan.Cases)
	notReplayable := sampled - estimate.Replayable
	lines = append(lines, fmt.Sprintf("Read %s in %s from %s (%s format).", plural(plan.File.Calls, "call"), plural(plan.File.Traces, "trace"), plan.Input.File, plan.Input.Adapter))
	if plan.File.SkippedLines > 0 {
		lines = append(lines, fmt.Sprintf("Skipped %s that could not be read.", plural(plan.File.SkippedLines, "line")))
	}
	tail := "."
	if notReplayable > 0 {
		tail = fmt.Sprintf(", %d cannot:", notReplayable)
	}
	lines = append(lines, fmt.Sprintf("Sampled the latest %s (up to %d replayable, one per trace): %d can be replayed%s", plural(sampled, "case"), settings.Sample, estimate.Replayable, tail))
	type count struct {
		reason string
		n      int
		order  int
	}
	var counts []count
	for reason, n := range estimate.NotReplayable {
		order := slices.Index(reasonOrder, types.NotReplayableReason(reason))
		if order < 0 {
			order = len(reasonOrder)
		}
		counts = append(counts, count{reason, n, order})
	}
	slices.SortStableFunc(counts, func(a, b count) int {
		if a.n != b.n {
			return b.n - a.n
		}
		if a.order != b.order {
			return a.order - b.order
		}
		return strings.Compare(a.reason, b.reason)
	})
	for _, c := range counts {
		text, ok := reasons[types.NotReplayableReason(c.reason)]
		if !ok {
			text = c.reason
		}
		lines = append(lines, fmt.Sprintf("  %d because %s", c.n, text))
	}

	floor := plan.FloorEndpoint != ""
	perCase := len(plan.Treatments)
	also := ""
	if floor {
		perCase += 2
		also = ", plus twice on production's model for the noise floor"
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Each replayable case runs on %s%s: %s in all.", plural(len(plan.Treatments), "model"), also, plural(estimate.Replayable*perCase, "call")))
	free := func(endpoint string) bool { return slices.Contains(plan.FreeEndpoints, endpoint) }
	for _, t := range plan.Treatments {
		where := "via " + t.Endpoint
		if free(t.Endpoint) {
			where = "local, free"
		}
		cost := ""
		if slices.Contains(estimate.Unpriced, t.Model) {
			cost = "no price in the catalog"
		} else if arm, ok := estimate.PerArm[t.ID]; ok {
			cost = "about " + usd(arm.ExpectedUSD) + ", at most " + usd(arm.UpperBoundUSD)
		}
		line := fmt.Sprintf("  %s  %s (%s)", t.ID, t.Label, where)
		if cost != "" {
			line += ": " + cost
		}
		lines = append(lines, line)
	}
	if floor {
		a1, a2 := estimate.PerArm[types.ArmFloorA1], estimate.PerArm[types.ArmFloorA2]
		where := "via " + plan.FloorEndpoint
		if free(plan.FloorEndpoint) {
			where = "local, free"
		}
		lines = append(lines, fmt.Sprintf("  noise floor, production's model twice (%s): about %s, at most %s", where, usd(a1.ExpectedUSD+a2.ExpectedUSD), usd(a1.UpperBoundUSD+a2.UpperBoundUSD)))
	}
	lines = append(lines, "")
	lines = append(lines, "Expected spend: about "+usd(estimate.ExpectedUSD)+" (production's token counts, with a 25% margin).")
	lines = append(lines, "Most it can spend: "+usd(estimate.UpperBoundUSD)+" (every call using its full max_tokens). Budget: "+usd(settings.BudgetUSD)+".")
	if estimate.ExpectedUSD > settings.BudgetUSD {
		lines = append(lines, "The expected spend is over the budget, so run will refuse. Raise --budget or lower --sample.")
	} else if estimate.UpperBoundUSD > settings.BudgetUSD {
		lines = append(lines, "The run reserves each call's maximum before sending it and stops at the budget, so it may replay fewer cases.")
	}
	if len(estimate.Unpriced) > 0 {
		lines = append(lines, "")
		lines = append(lines, "No price for: "+strings.Join(estimate.Unpriced, ", ")+". Their calls are not in the estimate.")
		if o.allowUnpriced {
			lines = append(lines, "They run anyway (--allow-unpriced): spend the endpoint reports still counts toward the budget.")
		} else {
			lines = append(lines, "Run refuses them unless you pass --allow-unpriced. Add a price: "+o.pricingHelpURL)
		}
	}
	return strings.Join(lines, "\n")
}

type replayCounts struct{ ok, failed, skipped int }

func countReplays(r *types.RunFile) replayCounts {
	var c replayCounts
	for _, replay := range r.Replays {
		switch replay.Status {
		case "ok":
			c.ok++
		case "failed":
			c.failed++
		case "skipped":
			c.skipped++
		}
	}
	return c
}

func explainOutcome(r *types.RunFile) string {
	counts := countReplays(r)
	var codes []string
	errors := map[string]int{}
	for _, replay := range r.Replays {
		if replay.Status != "failed" {
			continue
		}
		code := "failed"
		if replay.Error != nil {
			code = replay.Error.Code
		}
		if errors[code] == 0 {
			codes = append(codes, code)
		}
		errors[code]++
	}
	line := fmt.Sprintf("Replayed %s: %d answered, %d failed", plural(counts.ok+counts.failed, "call"), counts.ok, counts.failed)
	if counts.skipped > 0 {
		line += fmt.Sprintf(", %d not sent", counts.skipped)
	}
	line += ". Spent " + usd(r.SpentUSD) + " of " + usd(r.Settings.BudgetUSD) + "."
	lines := []string{line}
	if len(codes) > 0 {
		parts := make([]string, len(codes))
		for i, code := range codes {
			parts[i] = fmt.Sprintf("%s (%d)", code, errors[code])
		}
		lines = append(lines, "Failures: "+strings.Join(parts, ", ")+".")
	}
	if r.Stopped != nil {
		lines = append(lines, "Stopped early: "+tui.StopReasons[*r.Stopped]+".")
	}
	return strings.Join(lines, "\n")
}

// explainNotes are the report's notes (report.Caveats), for --explain: a
// heading, then each note wrapped as its own paragraph.
func explainNotes(notes []string, width int, p term.Paint, ascii bool) string {
	w := max(40, min(140, width))
	lines := []string{p.Muted("NOTES")}
	for i, note := range notes {
		if i > 0 {
			lines = append(lines, "")
		}
		for _, l := range term.Wrap(term.CleanLine(note), w-2) {
			lines = append(lines, "  "+l)
		}
	}
	text := strings.Join(lines, "\n")
	if ascii {
		text = term.ToAscii(text)
	}
	return text
}
