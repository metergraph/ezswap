package report

// The sensitivity map: for each part of an app, how many recorded answers
// change under a treatment, set against how many change when production's
// own model is simply run again. It is worked out from run.json each time it
// prints, as every other figure in the report is.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/metergraph/ezswap/internal/core"
	"github.com/metergraph/ezswap/internal/parts"
	"github.com/metergraph/ezswap/internal/types"
)

// Results, the keys the report already uses.
const (
	ResultWithinNoise   = "within_noise"
	ResultBeyondNoise   = "beyond_noise"
	ResultTooFewCalls   = "too_few_calls"
	ResultNotComparable = "not_comparable"

	ReasonNoFloor = "no noise floor"

	// JudgedPath stands for the path in a judged free-text part's changes:
	// the judge compares the whole reply.
	JudgedPath = "meaning"
)

// ErrNotMapRun is report --map on a run that `map` did not make.
var ErrNotMapRun = errors.New("This run has no parts. Make one with: ezswap map <traces> --models <list>")

// IsMapRun is whether `map` made this run.
func IsMapRun(run *types.RunFile) bool {
	return run != nil && run.Settings.PerPart != nil
}

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// MapOptions rename or narrow the map for one printing.
type MapOptions struct {
	// --name flags, "KEY=NAME".
	Rename []string
	// --only values: names or keys.
	Only []string
}

// MapPathCount is one compared path's counts within a part.
type MapPathCount struct {
	Path    string `json:"path"`
	Changed int    `json:"changed"`
	Rerun   int    `json:"rerun"`
}

// MapChange is one case that changed, at the first path where it did.
type MapChange struct {
	CaseID string `json:"caseId"`
	Path   string `json:"path"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// MapTreatment is one part's figures for one treatment.
type MapTreatment struct {
	// A, B, ...
	Label string `json:"label"`
	Model string `json:"model"`
	// Cases with a reply from the treatment and from production's rerun.
	Paired int `json:"paired"`
	// Treatment calls that failed.
	Failed int `json:"failed"`
	// Paired cases where the treatment differs from the recording.
	Changed int `json:"changed"`
	// Paired cases where production's rerun differs from the recording.
	Rerun int `json:"rerun"`
	// Paired cases where production's two reruns differ from each other.
	Itself int `json:"itself"`
	// Changed and not rerun, and the reverse: what the paired test reads.
	OnlyTreatment int    `json:"onlyTreatment"`
	OnlyRerun     int    `json:"onlyRerun"`
	Result        string `json:"result"`
	// For too_few_calls and not_comparable, why.
	Reason *string `json:"reason"`
	// The treatment's mean cost per call and median latency over the paired
	// cases, as a change from production's rerun. Nil when unknown.
	CostPerCallChange *float64 `json:"costPerCallChange"`
	LatencyChange     *float64 `json:"latencyChange"`

	// Paired cases the treatment's reply was cut off at the output cap,
	// and of those, the ones where reasoning took most of the output.
	CutOff          int `json:"cutOff"`
	CutOffReasoning int `json:"cutOffReasoning"`

	// A free-text part compared by a judge, and the cases where its
	// verdict changed with the order of the two replies.
	Judged bool `json:"judged,omitempty"`
	Unsure int  `json:"unsure,omitempty"`

	Paths   []MapPathCount `json:"pathCounts"`
	Changes []MapChange    `json:"changes"`
	Notes   []string       `json:"notes"`
}

// MapPart is one row of the map.
type MapPart struct {
	Keys    []string `json:"keys"`
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Sampled int      `json:"sampled"`
	// Cases whose recorded answer has no value at any compared path.
	LeftOut    int            `json:"leftOut"`
	Paths      []string       `json:"paths"`
	Treatments []MapTreatment `json:"treatments"`
}

// Map is everything the printed map shows.
type Map struct {
	File       string            `json:"-"`
	From       string            `json:"-"`
	To         string            `json:"-"`
	PerPart    int               `json:"-"`
	SpentUSD   float64           `json:"-"`
	BudgetUSD  float64           `json:"-"`
	Stopped    *string           `json:"-"`
	Treatments []types.Treatment `json:"-"`
	// The judge of free-text parts, or "".
	JudgeModel string    `json:"-"`
	Parts      []MapPart `json:"parts"`
}

// reading is one output read as JSON.
type reading struct {
	ok    bool
	value any
}

func read(output *string) reading {
	if output == nil {
		return reading{}
	}
	parsed := core.ParseOutput(*output)
	return reading{ok: parsed.OK, value: parsed.Value}
}

// at is the value at a path. An output that is not JSON, a missing key and a
// JSON null all read as "no value".
func (r reading) at(path string) (any, bool) {
	if !r.ok {
		return nil, false
	}
	value, ok := core.ValueAt(r.value, path)
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

// answers is whether the output holds the path at all, as a value or as an
// explicit JSON null: a recorded null is the answer "none".
func (r reading) answers(path string) bool {
	if !r.ok {
		return false
	}
	_, ok := core.ValueAt(r.value, path)
	return ok
}

// sameValue is whether two values at one path are the same answer. Types
// must match: the text "5" is not the number 5. Text is compared after
// trimming spaces; lists in order, item by item.
func sameValue(a, b any) bool {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && strings.TrimSpace(x) == strings.TrimSpace(y)
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameValue(x[i], y[i]) {
				return false
			}
		}
		return true
	case nil:
		return b == nil
	}
	// Objects: the same keys with the same values, in any order.
	var left, right any
	if json.Unmarshal([]byte(core.Stringify(a, "")), &left) != nil || json.Unmarshal([]byte(core.Stringify(b, "")), &right) != nil {
		return false
	}
	if _, isObject := left.(map[string]any); !isObject {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// differs is whether two readings differ at a path. No value on both sides
// is the same; a value on one side only is a difference.
func differs(a, b reading, path string) bool {
	x, xok := a.at(path)
	y, yok := b.at(path)
	if !xok && !yok {
		return false
	}
	if xok != yok {
		return true
	}
	return !sameValue(x, y)
}

// mostlyReasoning says reasoning took at least half of a reply's output.
func mostlyReasoning(u *types.Usage) bool {
	if u == nil || u.ReasoningTokens == nil || u.OutputTokens == nil || *u.OutputTokens <= 0 {
		return false
	}
	return *u.ReasoningTokens*2 >= *u.OutputTokens
}

func ok(r *types.Replay) bool { return r != nil && r.Status == "ok" }

// unreadable names why a reply has no JSON to compare, or "".
func unreadable(r *types.Replay) string {
	switch {
	case r.Refusal:
		return "refused"
	case r.FinishReason != nil && (*r.FinishReason == "length" || *r.FinishReason == "max_tokens"):
		return "cut off"
	case !read(r.Output).ok:
		return "reply not JSON"
	}
	return ""
}

func showAt(r reading, path string) string {
	value, present := r.at(path)
	if !present {
		return "(no value)"
	}
	return core.Stringify(value, "")
}

func judgeKey(caseID, arm, against string) string {
	return caseID + "\x00" + arm + "\x00" + against
}

// judgedText is a judged change as WHAT CHANGED shows it.
func judgedText(j *types.Judgement) string {
	text := fmt.Sprintf("judged different (%.0f%%)", j.PDifferent*100)
	if j.Unsure {
		text += ", unsure"
	}
	return text
}

// FreeTextCases is the cases of a map run's free-text parts: those a judge
// can compare. Parts are grouped as the map prints them with no options.
func FreeTextCases(run *types.RunFile) ([]types.Case, error) {
	if !IsMapRun(run) {
		return nil, ErrNotMapRun
	}
	groups, err := groupCases(run, MapOptions{})
	if err != nil {
		return nil, err
	}
	var out []types.Case
	for _, group := range groups {
		outputs := make([]*string, len(group.cases))
		for i, kase := range group.cases {
			outputs[i] = kase.Production.Output
		}
		if _, reason := parts.ComparedPaths(outputs, run.Settings.TrackedFields, run.Settings.IgnoredFields); reason == parts.ReasonFreeText {
			out = append(out, group.cases...)
		}
	}
	return out, nil
}

// ShortPath is a path as the map prints it: without the leading "$.".
func ShortPath(path string) string {
	return strings.TrimPrefix(strings.TrimPrefix(path, "$."), "$")
}

type mapGroup struct {
	name  string
	keys  []string
	cases []types.Case
}

// groupCases groups a map run's cases by part name, in the order parts first
// appear (the run holds them largest part first).
func groupCases(run *types.RunFile, options MapOptions) ([]mapGroup, error) {
	names := map[string]string{}
	var list []parts.Part
	index := map[string]int{}
	for _, kase := range run.Cases {
		if kase.PartKey == "" {
			continue
		}
		if _, seen := names[kase.PartKey]; seen {
			continue
		}
		names[kase.PartKey] = kase.PartName
		i, found := index[kase.PartName]
		if !found {
			i = len(list)
			index[kase.PartName] = i
			list = append(list, parts.Part{Name: kase.PartName})
		}
		list[i].Keys = append(list[i].Keys, kase.PartKey)
	}
	if len(list) == 0 {
		return nil, ErrNotMapRun
	}
	if len(options.Rename) > 0 {
		renamed, err := parts.Rename(list, options.Rename)
		if err != nil {
			return nil, err
		}
		for key, name := range renamed {
			names[key] = name
		}
	}
	var groups []mapGroup
	at := map[string]int{}
	for _, kase := range run.Cases {
		if kase.PartKey == "" {
			continue
		}
		name := names[kase.PartKey]
		i, found := at[name]
		if !found {
			i = len(groups)
			at[name] = i
			groups = append(groups, mapGroup{name: name})
		}
		if !slices.Contains(groups[i].keys, kase.PartKey) {
			groups[i].keys = append(groups[i].keys, kase.PartKey)
		}
		groups[i].cases = append(groups[i].cases, kase)
	}
	if len(options.Only) > 0 {
		list = list[:0]
		for _, g := range groups {
			list = append(list, parts.Part{Name: g.name, Keys: g.keys})
		}
		kept, err := parts.Select(list, options.Only)
		if err != nil {
			return nil, err
		}
		var out []mapGroup
		for _, g := range groups {
			if slices.ContainsFunc(kept, func(p parts.Part) bool { return p.Name == g.name }) {
				out = append(out, g)
			}
		}
		groups = out
	}
	return groups, nil
}

func sourceOf(keys []string) string {
	named, fingerprinted := false, false
	for _, key := range keys {
		if strings.HasPrefix(key, parts.NamePrefix) {
			named = true
		} else {
			fingerprinted = true
		}
	}
	switch {
	case named && fingerprinted:
		return parts.SourceMixed
	case named:
		return parts.SourceName
	}
	return parts.SourceFingerprint
}

func mean(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func relativeChange(treatment, base []float64, of func([]float64) float64) *float64 {
	if len(treatment) == 0 || len(base) == 0 {
		return nil
	}
	b := of(base)
	if b <= 0 {
		return nil
	}
	c := of(treatment)/b - 1
	return &c
}

// BuildMap works the map out from a run that `map` made.
func BuildMap(run *types.RunFile, options MapOptions) (*Map, error) {
	if !IsMapRun(run) {
		return nil, ErrNotMapRun
	}
	groups, err := groupCases(run, options)
	if err != nil {
		return nil, err
	}
	replays := map[string]map[string]*types.Replay{}
	for i := range run.Replays {
		r := &run.Replays[i]
		if replays[r.CaseID] == nil {
			replays[r.CaseID] = map[string]*types.Replay{}
		}
		replays[r.CaseID][r.Arm] = r
	}
	m := &Map{
		File:       run.Input.File,
		PerPart:    *run.Settings.PerPart,
		SpentUSD:   run.SpentUSD,
		BudgetUSD:  run.Settings.BudgetUSD,
		Stopped:    run.Stopped,
		Treatments: run.Treatments,
	}
	var verdicts map[string]*types.Judgement
	if run.Judge != nil {
		m.JudgeModel = run.Judge.Model
		verdicts = map[string]*types.Judgement{}
		for i := range run.Judge.Judgements {
			j := &run.Judge.Judgements[i]
			verdicts[judgeKey(j.CaseID, j.Arm, j.Against)] = j
		}
	}
	for _, kase := range run.Cases {
		if kase.PartKey == "" || kase.TS == nil || len(*kase.TS) < 10 || !dayPattern.MatchString((*kase.TS)[:10]) {
			continue
		}
		day := (*kase.TS)[:10]
		if m.From == "" || day < m.From {
			m.From = day
		}
		if day > m.To {
			m.To = day
		}
	}
	for _, group := range groups {
		outputs := make([]*string, len(group.cases))
		for i, kase := range group.cases {
			outputs[i] = kase.Production.Output
		}
		paths, reason := parts.ComparedPaths(outputs, run.Settings.TrackedFields, run.Settings.IgnoredFields)
		keys := append([]string(nil), group.keys...)
		sort.Strings(keys)
		part := MapPart{Keys: keys, Name: group.name, Source: sourceOf(keys), Sampled: len(group.cases), Paths: paths}
		if part.Paths == nil {
			part.Paths = []string{}
		}
		// The paths each case is counted at: those where the recording has a value.
		counted := make([][]string, len(group.cases))
		controls := make([]reading, len(group.cases))
		for i, kase := range group.cases {
			controls[i] = read(kase.Production.Output)
			for _, path := range paths {
				if controls[i].answers(path) {
					counted[i] = append(counted[i], path)
				}
			}
			if len(paths) > 0 && len(counted[i]) == 0 {
				part.LeftOut++
			}
		}
		for ti, treatment := range run.Treatments {
			part.Treatments = append(part.Treatments,
				mapTreatment(run, group, treatment, Letter(ti), paths, reason, counted, controls, replays, verdicts))
		}
		m.Parts = append(m.Parts, part)
	}
	return m, nil
}

func mapTreatment(
	run *types.RunFile, group mapGroup, treatment types.Treatment, letter string,
	paths []string, reason string, counted [][]string, controls []reading,
	replays map[string]map[string]*types.Replay, verdicts map[string]*types.Judgement,
) MapTreatment {
	out := MapTreatment{Label: letter, Model: treatment.Model, Paths: []MapPathCount{}, Changes: []MapChange{}, Notes: []string{}}
	// A free-text part is compared by the judge's verdicts, when there are any.
	judged := reason == parts.ReasonFreeText && verdicts != nil
	out.Judged = judged
	pathChanged, pathRerun := map[string]int{}, map[string]int{}
	pathOnlyT, pathOnlyR := map[string]int{}, map[string]int{}
	var costT, costA, latT, latA []float64
	staleOnly, itselfOnly := 0, 0
	for i, kase := range group.cases {
		arms := replays[kase.ID]
		t, a1, a2 := arms[treatment.ID], arms[types.ArmFloorA1], arms[types.ArmFloorA2]
		if t != nil && t.Status == "failed" {
			out.Failed++
		}
		if !ok(t) || (run.Settings.NoiseFloor && !ok(a1)) {
			continue
		}
		if len(paths) > 0 && len(counted[i]) == 0 {
			continue // left out: the recording has nothing to compare
		}
		var jt, jr, ji *types.Judgement
		if judged {
			jt = verdicts[judgeKey(kase.ID, treatment.ID, types.JudgedAgainstRecording)]
			if ok(a1) {
				jr = verdicts[judgeKey(kase.ID, types.ArmFloorA1, types.JudgedAgainstRecording)]
				if ok(a2) {
					ji = verdicts[judgeKey(kase.ID, types.ArmFloorA2, types.ArmFloorA1)]
				}
			}
			if jt == nil || (ok(a1) && jr == nil) {
				continue // not judged: the judge failed or was not asked
			}
		}
		if ok(a1) {
			if t.CostUSD != nil && a1.CostUSD != nil {
				costT, costA = append(costT, *t.CostUSD), append(costA, *a1.CostUSD)
			}
			if t.LatencyMs != nil && a1.LatencyMs != nil {
				latT, latA = append(latT, *t.LatencyMs), append(latA, *a1.LatencyMs)
			}
		}
		out.Paired++
		if unreadable(t) == "cut off" {
			out.CutOff++
			if mostlyReasoning(t.Usage) {
				out.CutOffReasoning++
			}
		}
		tally := func(changed, rerun, itself bool) {
			if rerun {
				out.Rerun++
			}
			if itself {
				out.Itself++
			}
			switch {
			case changed && !rerun:
				out.OnlyTreatment++
			case rerun && !changed:
				out.OnlyRerun++
			}
			switch {
			case rerun && !itself:
				staleOnly++
			case itself && !rerun:
				itselfOnly++
			}
		}
		if judged {
			changed := jt.PDifferent > 0.5
			if jt.Unsure {
				out.Unsure++
			}
			if changed {
				out.Changed++
				out.Changes = append(out.Changes, MapChange{CaseID: kase.ID, Path: JudgedPath, From: "", To: judgedText(jt)})
			}
			tally(changed, jr != nil && jr.PDifferent > 0.5, ji != nil && ji.PDifferent > 0.5)
			continue
		}
		if len(paths) == 0 {
			continue
		}
		reply := read(t.Output)
		changed, rerun, itself := false, false, false
		firstPath := ""
		var first reading
		if ok(a1) {
			first = read(a1.Output)
		}
		for _, path := range counted[i] {
			here := differs(controls[i], reply, path)
			there := ok(a1) && differs(controls[i], first, path)
			if here {
				pathChanged[path]++
				if !changed {
					firstPath = path
				}
				changed = true
			}
			if there {
				pathRerun[path]++
				rerun = true
			}
			switch {
			case here && !there:
				pathOnlyT[path]++
			case there && !here:
				pathOnlyR[path]++
			}
			if ok(a1) && ok(a2) && differs(first, read(a2.Output), path) {
				itself = true
			}
		}
		if changed {
			out.Changed++
			to := unreadable(t)
			if to == "" {
				to = showAt(reply, firstPath)
			}
			out.Changes = append(out.Changes, MapChange{CaseID: kase.ID, Path: firstPath, From: showAt(controls[i], firstPath), To: to})
		}
		tally(changed, rerun, itself)
	}
	for _, path := range paths {
		out.Paths = append(out.Paths, MapPathCount{Path: path, Changed: pathChanged[path], Rerun: pathRerun[path]})
	}
	out.CostPerCallChange = relativeChange(costT, costA, mean)
	out.LatencyChange = relativeChange(latT, latA, median)

	set := func(result, why string) {
		out.Result = result
		if why != "" {
			out.Reason = &why
		}
	}
	switch {
	case reason != "" && reason != parts.ReasonTooFew && !judged:
		set(ResultNotComparable, reason)
	case !run.Settings.NoiseFloor:
		set(ResultNotComparable, ReasonNoFloor)
	case out.Paired < parts.MinCases:
		set(ResultTooFewCalls, fmt.Sprintf("%d paired, needs %d", out.Paired, parts.MinCases))
	case PairedTest(out.OnlyTreatment, out.OnlyRerun):
		set(ResultBeyondNoise, "")
	default:
		set(ResultWithinNoise, "")
	}
	if out.Result != ResultBeyondNoise && out.Result != ResultWithinNoise {
		out.Changes = []MapChange{}
		return out
	}
	if judged && out.Rerun*2 > out.Paired {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%s: the judge found a rerun of production's own model saying something different from its recording in %d of %d cases. A treatment can hardly exceed that.",
			group.name, out.Rerun, out.Paired))
	}
	if judged && out.Unsure > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%s: the judge's verdict changed with the order of the two replies in %d of %d cases; each counts by the mean of both orders.",
			group.name, out.Unsure, out.Paired))
	}
	if !judged && out.Rerun*2 > out.Paired {
		worst := out.Paths[0]
		for _, p := range out.Paths {
			if p.Rerun > worst.Rerun {
				worst = p
			}
		}
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%s: a rerun of production's own model differs from its recording in %d of %d cases, most often at %s. A treatment can hardly exceed that; try --ignore '%s'.",
			group.name, out.Rerun, out.Paired, ShortPath(worst.Path), worst.Path))
	}
	if PairedTest(staleOnly, itselfOnly) {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%s: production's own model no longer reproduces its recording (%d of %d cases, against %d between two reruns). The recording may be old, or a setting was not replayed.",
			group.name, out.Rerun, out.Paired, out.Itself))
	}
	if out.Result == ResultWithinNoise {
		for _, p := range out.Paths {
			if PairedTest(pathOnlyT[p.Path], pathOnlyR[p.Path]) {
				out.Notes = append(out.Notes, fmt.Sprintf(
					"%s: %s changed in %d cases against %d on a rerun, but other fields raise the part's baseline.",
					group.name, ShortPath(p.Path), p.Changed, p.Rerun))
			}
		}
	}
	return out
}
