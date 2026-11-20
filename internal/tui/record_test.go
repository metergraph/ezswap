package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/metergraph/ezswap/internal/record"
	"github.com/metergraph/ezswap/internal/report/term"
)

func step(m Recording, msgs ...tea.Msg) Recording {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Recording)
	}
	return m
}

func TestRecordingCountsCallsByPart(t *testing.T) {
	stops := 0
	at := time.Date(2026, 11, 19, 16, 40, 0, 0, time.UTC)
	m := NewRecording(RecordingOptions{
		Paint: term.PlainPaint(), Width: 90, File: "ezswap-out/traces.jsonl", Upstream: "https://gateway.example.com",
		SetAside: "ezswap-out/traces-20261119-164000.jsonl", Stop: func() { stops++ }, Now: func() time.Time { return at },
	})
	if !strings.Contains(m.Frame(), "Starting...") {
		t.Fatalf("before ready:\n%s", m.Frame())
	}
	if strings.Contains(m.Frame(), "step ") {
		t.Errorf("a step is shown outside the guided workflow:\n%s", m.Frame())
	}
	guided := NewRecording(RecordingOptions{Paint: term.PlainPaint(), Width: 90, ASCII: true, Step: "step 1 of 3"})
	if first := strings.SplitN(guided.Frame(), "\n", 2)[0]; !strings.Contains(first, "ezswap record  step 1 of 3") {
		t.Errorf("title lacks the step: %q", first)
	}
	m = step(m, RecordReadyMsg{Address: "127.0.0.1:8787"})
	if !strings.Contains(m.Frame(), "Waiting for the first call.") || !strings.Contains(m.Frame(), "http://127.0.0.1:8787/v1") {
		t.Fatalf("ready:\n%s", m.Frame())
	}
	for i := 0; i < 3; i++ {
		m = step(m, RecordEventMsg{Event: record.Event{Path: "/v1/chat/completions", Part: "route-ticket", Outcome: "recorded"}})
	}
	m = step(m,
		RecordEventMsg{Event: record.Event{Path: "/v1/chat/completions", Part: "draft-reply", Outcome: "recorded"}},
		RecordEventMsg{Event: record.Event{Path: "/v1/chat/completions", Part: "draft-reply", Outcome: "stream cut"}},
		RecordEventMsg{Event: record.Event{Path: "/v1/chat/completions", Part: "\x1b]0;pwned\x07evil", Outcome: "recorded"}},
	)
	frame := m.Frame()
	for _, want := range []string{
		"ezswap record", "Started fresh. The previous recording is now ezswap-out/traces-20261119-164000.jsonl.",
		"route-ticket", "draft-reply", "5 calls recorded", "not recorded: 1 stream cut", "LATEST", "16:40:00  stream cut",
		"q or Ctrl-C stops recording",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "\x1b]") || strings.Contains(frame, "\x07") {
		t.Error("a part name drove the terminal")
	}
	for _, l := range strings.Split(frame, "\n") {
		if term.Width(l) > 90 {
			t.Errorf("line wider than 90: %q", l)
		}
	}
	if m.Recorded() != 5 {
		t.Errorf("recorded %d", m.Recorded())
	}
	// q stops the recorder once; the view waits for it to return.
	m = step(m, tea.KeyPressMsg{Code: 'q', Text: "q"}, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if stops != 1 || !m.Stopping() || !strings.Contains(m.Frame(), "stopping") {
		t.Errorf("stops %d, stopping %v", stops, m.Stopping())
	}
	next, cmd := m.Update(DoneMsg{})
	if cmd == nil || next.(Recording).View().Content != "" {
		t.Error("the view did not end on DoneMsg")
	}
}

func TestRecordingLatestKeepsSix(t *testing.T) {
	m := step(NewRecording(RecordingOptions{Paint: term.PlainPaint(), Width: 80, ASCII: true}), RecordReadyMsg{Address: "127.0.0.1:1"})
	for i := 0; i < 10; i++ {
		m = step(m, RecordEventMsg{Event: record.Event{Path: "/v1/chat/completions", Part: "p", Outcome: "recorded"}})
	}
	latest := strings.SplitN(m.Frame(), "LATEST", 2)[1]
	if n := strings.Count(latest, "recorded "); n != recentCalls {
		t.Errorf("%d latest calls shown:\n%s", n, latest)
	}
	for _, r := range m.Frame() {
		if r > 0x7e && r != '\n' {
			t.Fatalf("ASCII frame holds %q", r)
		}
	}
}
