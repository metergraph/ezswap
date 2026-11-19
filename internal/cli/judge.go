package cli

// Judging free-text parts: `map --judge`, and `judge <run.json>` for a map
// run already made. The verdicts are stored in run.json, so printing the map
// again asks nothing of the network.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/metergraph/replay/internal/judge"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/run"
	"github.com/metergraph/replay/internal/types"
)

// judgeNote is said before any reply is sent to the judge.
const judgeNote = "The request and both replies of each pair go to the AI Gateway, which passes them to the judge's provider with zero data retention asked for."

type judged struct {
	pairs   int
	failed  int
	costUSD float64
	// The first failure, for the message.
	firstErr error
}

type pendingPair struct {
	judgement types.Judgement
	pair      judge.Pair
}

// judgeRequest is what the replies answer: the instructions and the
// messages, as the judge reads them.
func judgeRequest(kase types.Case) string {
	var b strings.Builder
	if kase.System != nil && strings.TrimSpace(*kase.System) != "" {
		b.WriteString("Instructions to the assistant: " + *kase.System + "\n")
	}
	for _, m := range kase.Messages {
		b.WriteString(m.Role + ": " + m.Content + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// pendingPairs is every pair of free-text replies the run's judge has not
// yet judged: each treatment and production's first rerun against the
// recording, and production's second rerun against its first.
func pendingPairs(r *types.RunFile, model string) ([]pendingPair, error) {
	cases, err := report.FreeTextCases(r)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	if r.Judge != nil && r.Judge.Model == model {
		for _, j := range r.Judge.Judgements {
			have[j.CaseID+"\x00"+j.Arm+"\x00"+j.Against] = true
		}
	}
	replies := map[string]map[string]*types.Replay{}
	for i := range r.Replays {
		reply := &r.Replays[i]
		if reply.Status != "ok" {
			continue
		}
		if replies[reply.CaseID] == nil {
			replies[reply.CaseID] = map[string]*types.Replay{}
		}
		replies[reply.CaseID][reply.Arm] = reply
	}
	text := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	var out []pendingPair
	for _, kase := range cases {
		if kase.Production.Output == nil || kase.NotReplayable != nil {
			continue
		}
		arms := replies[kase.ID]
		request := judgeRequest(kase)
		add := func(arm, against, a, b string) {
			if have[kase.ID+"\x00"+arm+"\x00"+against] {
				return
			}
			out = append(out, pendingPair{
				judgement: types.Judgement{CaseID: kase.ID, Arm: arm, Against: against},
				pair:      judge.Pair{Request: request, A: a, B: b},
			})
		}
		recorded := *kase.Production.Output
		for _, t := range r.Treatments {
			if reply := arms[t.ID]; reply != nil {
				add(t.ID, types.JudgedAgainstRecording, recorded, text(reply.Output))
			}
		}
		if a1 := arms[types.ArmFloorA1]; a1 != nil {
			add(types.ArmFloorA1, types.JudgedAgainstRecording, recorded, text(a1.Output))
			if a2 := arms[types.ArmFloorA2]; a2 != nil {
				add(types.ArmFloorA2, types.ArmFloorA1, text(a1.Output), text(a2.Output))
			}
		}
	}
	return out, nil
}

// judgeKey is the AI Gateway key, or a usage error that names it.
func (s *session) judgeKey(command string) (string, error) {
	key := strings.TrimSpace(s.env(judge.KeyEnv))
	if key == "" {
		return "", &cliError{ExitRefused, "credential_missing", command + " --judge asks a judge through the AI Gateway and needs " + judge.KeyEnv + ". Set it, or leave out --judge."}
	}
	return key, nil
}

// judgeRun asks the judge about the run's free-text replies and stores the
// verdicts in it. A pair the judge could not answer is left out; its case
// drops out of the part.
func (s *session) judgeRun(ctx context.Context, r *types.RunFile, key string) (judged, error) {
	model := judge.DefaultModel
	pending, err := pendingPairs(r, model)
	if err != nil {
		return judged{}, err
	}
	result := judged{pairs: len(pending)}
	if len(pending) == 0 {
		return result, nil
	}
	s.say(fmt.Sprintf("Judging %s of free-text replies with %s, each in both orders.", plural(len(pending), "pair"), model))
	s.say(judgeNote)
	pairs := make([]judge.Pair, len(pending))
	for i, p := range pending {
		pairs[i] = p.pair
	}
	options := judge.Options{BaseURL: s.io.JudgeURL, Model: model, APIKey: key}
	showProgress := s.io.StderrTTY && !s.asJSON
	if showProgress {
		options.OnProgress = func(done, total int) {
			fmt.Fprintf(s.io.Stderr, "\r  judged %d/%d pairs   ", done, total)
		}
	}
	verdicts := judge.Judge(ctx, pairs, options)
	if showProgress {
		io.WriteString(s.io.Stderr, "\n")
	}
	if r.Judge == nil || r.Judge.Model != model {
		r.Judge = &types.JudgeRecord{Model: model, Question: judge.Question, Judgements: []types.Judgement{}}
	}
	for i, v := range verdicts {
		if v.Err != nil {
			result.failed++
			if result.firstErr == nil {
				result.firstErr = v.Err
			}
			continue
		}
		j := pending[i].judgement
		j.PDifferent = v.PDifferent
		j.Unsure = v.Unsure
		r.Judge.Judgements = append(r.Judge.Judgements, j)
		result.costUSD += v.CostUSD
	}
	r.Judge.CostUSD += result.costUSD
	line := fmt.Sprintf("Judged %d of %d pairs for %s.", len(pending)-result.failed, len(pending), usd(result.costUSD))
	if result.failed > 0 {
		line += fmt.Sprintf(" The judge could not answer for %d (%v); their cases are left out of their parts. Run metergraph-replay judge on the run file to try them again.", result.failed, result.firstErr)
	}
	s.say(line)
	return result, nil
}

func (j judged) fields() object {
	return object{{"pairs", j.pairs}, {"failed", j.failed}, {"costUsd", j.costUSD}}
}

// judgeCommand is `judge <run.json>`: judge a map run's free-text parts and
// write the verdicts back into the file, then print the map.
func (s *session) judgeCommand(file string) (outcome, error) {
	for _, name := range append(append(append(append([]string{"out", "map", "judge"}, runOnlyOptions...), mapPlanOptions...), mapViewOptions...), recordOptions...) {
		if s.given(name) {
			return outcome{}, usagef("judge takes a run.json and no --" + name + ".")
		}
	}
	r, err := readRunFile(file)
	if err != nil {
		return outcome{}, err
	}
	if !report.IsMapRun(r) {
		return outcome{}, usagef(file + " was not made by map. Only a map's free-text parts are judged.")
	}
	key, err := s.judgeKey("judge")
	if err != nil {
		return outcome{}, err
	}
	interrupt := s.installInterrupt(false)
	result, err := s.judgeRun(interrupt.soft, r, key)
	interrupt.dispose()
	interrupt.stopSoft()
	interrupt.stopHard()
	if err != nil {
		return outcome{}, err
	}
	if result.pairs == 0 {
		s.say("Nothing to judge: the run has no free-text replies the judge has not seen.")
	} else if result.failed < result.pairs {
		if err := rewriteRun(file, r); err != nil {
			return outcome{}, err
		}
		s.say("Wrote " + s.shownPath(file))
	}
	s.say("")
	m, err := report.BuildMap(r, report.MapOptions{})
	if err != nil {
		return outcome{}, err
	}
	s.printMap(m)
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	exitCode := ExitOK
	if result.pairs > 0 && result.failed == result.pairs {
		exitCode = ExitError
	}
	return outcome{exitCode, object{{"command", "judge"}, {"runFile", abs}, {"judge", result.fields()}, {"parts", m.Parts}}}, nil
}

// rewriteRun replaces a run file whole, through a file beside it.
func rewriteRun(path string, r *types.RunFile) error {
	text, err := run.EncodeRun(r)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("Could not write %s: %w", path, err)
	}
	mode := os.FileMode(0o666)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	_, werr := temp.Write(text)
	cerr := temp.Close()
	if werr == nil && cerr == nil {
		werr = os.Chmod(temp.Name(), mode)
	}
	if werr != nil || cerr != nil {
		os.Remove(temp.Name())
		return fmt.Errorf("Could not write %s.", path)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		os.Remove(temp.Name())
		return fmt.Errorf("Could not write %s.", path)
	}
	return nil
}
