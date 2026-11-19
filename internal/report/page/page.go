// Package page writes a run as one self-contained HTML file: the map's
// summary and every case behind it, or the report of a plain run. The data,
// styles and script are inline; the page makes no network request, so it
// opens from disk and can be attached or sent as it is. It holds the
// replies from the trace file, so it goes only where the traces may go.
package page

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/types"
)

//go:embed page.css
var css string

//go:embed page.js
var script string

// Warning is what the CLI says when it writes a page.
const Warning = "The page holds the prompts and replies from the trace file: share it only where the traces may go."

type treatment struct {
	Letter   string `json:"letter"`
	Model    string `json:"model"`
	ID       string `json:"id"`
	Settings string `json:"settings"`
}

type reply struct {
	Status       string   `json:"status"`
	Output       *string  `json:"output"`
	FinishReason *string  `json:"finishReason"`
	Refusal      bool     `json:"refusal"`
	Error        *string  `json:"error"`
	CostUSD      *float64 `json:"costUsd"`
	LatencyMs    *float64 `json:"latencyMs"`
}

type pageCase struct {
	ID       string           `json:"id"`
	Part     string           `json:"part"`
	TS       *string          `json:"ts"`
	System   *string          `json:"system"`
	Messages []types.Message  `json:"messages"`
	Recorded reply            `json:"recorded"`
	Arms     map[string]reply `json:"arms"`
}

type summary struct {
	Order []string   `json:"order"`
	Cost  []*float64 `json:"cost"`
}

type mapData struct {
	Kind       string           `json:"kind"`
	File       string           `json:"file"`
	From       string           `json:"from"`
	To         string           `json:"to"`
	PerPart    int              `json:"perPart"`
	SpentUSD   float64          `json:"spentUsd"`
	BudgetUSD  float64          `json:"budgetUsd"`
	Stopped    *string          `json:"stopped"`
	Catalog    string           `json:"catalog"`
	Created    string           `json:"created"`
	Rule       string           `json:"rule"`
	Footer     string           `json:"footer"`
	Treatments []treatment      `json:"treatments"`
	Parts      []report.MapPart `json:"parts"`
	Summary    summary          `json:"summary"`
	Cases      []pageCase       `json:"cases"`
}

type runData struct {
	Kind   string        `json:"kind"`
	Model  *report.Model `json:"model"`
	Footer string        `json:"footer"`
}

// MapPage is the page for a map run.
func MapPage(run *types.RunFile, m *report.Map) ([]byte, error) {
	data := mapData{
		Kind: "map", File: m.File, From: m.From, To: m.To, PerPart: m.PerPart,
		SpentUSD: m.SpentUSD, BudgetUSD: m.BudgetUSD, Stopped: m.Stopped,
		Catalog: run.Catalog.Version, Created: run.CreatedAt,
		Rule: report.MapRule, Footer: report.MapFooter, Parts: withNotes(m.Parts),
	}
	sameEndpoint := true
	for _, t := range m.Treatments {
		sameEndpoint = sameEndpoint && t.Endpoint == m.Treatments[0].Endpoint
	}
	for i, t := range m.Treatments {
		if sameEndpoint {
			t.Endpoint = ""
		}
		data.Treatments = append(data.Treatments, treatment{Letter: report.Letter(i), Model: t.Label, ID: t.ID, Settings: settings(t)})
		data.Summary.Cost = append(data.Summary.Cost, report.SummaryCost(m, i))
	}
	for _, part := range report.SummaryParts(m) {
		data.Summary.Order = append(data.Summary.Order, part.Name)
	}
	// A case belongs to the part whose keys hold its key, under the name
	// the map shows (after renames and merges).
	partOf := map[string]string{}
	for _, part := range m.Parts {
		for _, key := range part.Keys {
			partOf[key] = part.Name
		}
	}
	replays := map[string]map[string]*types.Replay{}
	for i := range run.Replays {
		r := &run.Replays[i]
		if replays[r.CaseID] == nil {
			replays[r.CaseID] = map[string]*types.Replay{}
		}
		replays[r.CaseID][r.Arm] = r
	}
	for _, c := range run.Cases {
		name, ok := partOf[c.PartKey]
		if !ok || c.NotReplayable != nil {
			continue
		}
		pc := pageCase{
			ID: c.ID, Part: name, TS: c.TS, System: c.System, Messages: c.Messages,
			Recorded: reply{Status: "ok", Output: c.Production.Output, FinishReason: c.Production.FinishReason, Refusal: c.Production.Refusal},
			Arms:     map[string]reply{},
		}
		for arm, r := range replays[c.ID] {
			out := reply{Status: r.Status, Output: r.Output, FinishReason: r.FinishReason, Refusal: r.Refusal, CostUSD: r.CostUSD, LatencyMs: r.LatencyMs}
			if r.Error != nil {
				message := r.Error.Code
				if r.Error.Message != "" {
					message += ": " + r.Error.Message
				}
				out.Error = &message
			}
			pc.Arms[arm] = out
		}
		data.Cases = append(data.Cases, pc)
	}
	return assemble("Replay map: "+m.File, data)
}

// RunPage is the page for a plain run, from the report's model.
func RunPage(m *report.Model) ([]byte, error) {
	return assemble("Replay report: "+m.Headline, runData{Kind: "run", Model: m, Footer: "This compares models on the sampled calls. It does not say a change is better or worse."})
}

func assemble(title string, data any) ([]byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("page: %w", err)
	}
	// json.Marshal escapes <, > and & (and U+2028, U+2029), so the payload
	// cannot close the script element it sits in.
	var b bytes.Buffer
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	// No request leaves the page: scripts and styles are inline, images and
	// connections are refused.
	b.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:\">\n")
	b.WriteString("<meta name=\"generator\" content=\"metergraph-replay\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n<style>\n" + css + "</style>\n</head>\n<body>\n")
	b.WriteString("<main id=\"app\"><noscript>This report needs JavaScript to draw. Its data is in the page source.</noscript></main>\n")
	b.WriteString("<script type=\"application/json\" id=\"replay-data\">")
	b.Write(payload)
	b.WriteString("</script>\n<script>\n" + strings.TrimSpace(script) + "\n</script>\n</body>\n</html>\n")
	return b.Bytes(), nil
}

// withNotes copies the parts with the notes the printed map adds at print
// time, so the page says the same as the terminal.
func withNotes(parts []report.MapPart) []report.MapPart {
	out := make([]report.MapPart, len(parts))
	for i, part := range parts {
		part.Treatments = append([]report.MapTreatment(nil), part.Treatments...)
		for j, t := range part.Treatments {
			notes := append([]string(nil), t.Notes...)
			if t.CutOff > 0 {
				notes = append(notes, report.CutOffNote(part.Name, t))
			}
			part.Treatments[j].Notes = notes
		}
		out[i] = part
	}
	return out
}

// settings names what a treatment changed besides the model, or "".
func settings(t types.Treatment) string {
	var parts []string
	if t.Endpoint != "" {
		parts = append(parts, "via "+t.Endpoint)
	}
	if v := t.Settings.MaxTokens; v != nil {
		parts = append(parts, fmt.Sprintf("max tokens %g", *v))
	}
	if v := t.Settings.ReasoningEffort; v != nil {
		parts = append(parts, "reasoning "+*v)
	}
	if v := t.Settings.Temperature; v != nil {
		parts = append(parts, fmt.Sprintf("temperature %g", *v))
	}
	return strings.Join(parts, ", ")
}
