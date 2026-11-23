package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

var day = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func TestNothingIsOnUntilAnswered(t *testing.T) {
	dir := t.TempDir()
	if s, answered := Load(dir); answered || s.Enabled {
		t.Fatalf("an empty directory answered: %+v", s)
	}
	if s, answered := Load(""); answered || s.Enabled {
		t.Fatalf("no directory answered: %+v", s)
	}
	os.WriteFile(filepath.Join(dir, File), []byte("{not json"), 0o600)
	if s, answered := Load(dir); answered || s.Enabled {
		t.Fatalf("a broken file answered: %+v", s)
	}
	// On with no id cannot be sent, so it reads as off.
	os.WriteFile(filepath.Join(dir, File), []byte(`{"enabled":true}`), 0o600)
	if s, _ := Load(dir); s.Enabled {
		t.Fatal("on without an id")
	}
}

func TestAnswerKeepsTheIDWhileOnAndDropsItWhenOff(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ezswap")
	on, err := Answer(dir, true, day)
	if err != nil || !on.Enabled || len(on.ID) != 32 || on.Answered != "2026-11-22" {
		t.Fatalf("%+v %v", on, err)
	}
	again, _ := Answer(dir, true, day)
	if again.ID != on.ID {
		t.Error("turning it on twice changed the id")
	}
	off, _ := Answer(dir, false, day)
	if off.Enabled || off.ID != "" {
		t.Fatalf("%+v", off)
	}
	if loaded, answered := Load(dir); !answered || loaded.Enabled || loaded.ID != "" {
		t.Fatalf("%+v", loaded)
	}
	fresh, _ := Answer(dir, true, day)
	if fresh.ID == on.ID {
		t.Error("the id survived turning telemetry off")
	}
	info, err := os.Stat(filepath.Join(dir, File))
	// Windows has no owner-only permission bits to check.
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Errorf("%v %v", info, err)
	}
}

func TestOffBy(t *testing.T) {
	for env, want := range map[string]string{
		"":                     "",
		"DO_NOT_TRACK=1":       "DO_NOT_TRACK",
		"DO_NOT_TRACK=0":       "",
		"EZSWAP_TELEMETRY=0":   "EZSWAP_TELEMETRY",
		"EZSWAP_TELEMETRY=off": "EZSWAP_TELEMETRY",
		"EZSWAP_TELEMETRY=1":   "",
		"CI=true":              "CI",
	} {
		m := map[string]string{}
		if env != "" {
			k, v, _ := cut(env)
			m[k] = v
		}
		if got := OffBy(m); got != want {
			t.Errorf("%q: %q, want %q", env, got, want)
		}
	}
}

func cut(s string) (string, string, bool) {
	for i := range s {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// The whole set of keys an event may carry. A new field must be added here
// and to docs/telemetry.md.
var allowed = []string{
	"command", "version", "os", "arch", "exit_code", "seconds", "interactive",
	"error", "start", "objective", "production", "models", "parts", "cases", "judge",
	"$process_person_profile", "$geoip_disable",
}

func TestSendPostsOnlyTheDeclaredFields(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
	}))
	defer server.Close()
	e := Event{
		Command: "map", Version: "0.2.0", OS: "linux", Arch: "arm64", ExitCode: 4, Error: "over_budget", Seconds: 3,
		Interactive: true, Start: "analyze", Objective: "cost", Models: []string{"openai/gpt-6-luna", Other},
		Production: Other, Parts: 4, Cases: 80, Judge: true,
	}
	if err := Send(context.Background(), server.URL, "key", "abc", e, day); err != nil {
		t.Fatal(err)
	}
	if got["api_key"] != "key" || got["event"] != Name || got["distinct_id"] != "abc" || got["timestamp"] != "2026-11-22T10:00:00Z" {
		t.Fatalf("%v", got)
	}
	if len(got) != 5 {
		t.Errorf("the body has %d keys, want 5: %v", len(got), got)
	}
	p := got["properties"].(map[string]any)
	if len(p) != len(allowed) {
		t.Errorf("%d properties, want %d: %v", len(p), len(allowed), p)
	}
	for k := range p {
		if !slices.Contains(allowed, k) {
			t.Errorf("undeclared property %q", k)
		}
	}
	if p["$process_person_profile"] != false || p["$geoip_disable"] != true {
		t.Errorf("%v", p)
	}
}

func TestSendNeedsAKeyAnIDAndAURL(t *testing.T) {
	for _, c := range [][3]string{{"", "k", "i"}, {"http://127.0.0.1:1", "", "i"}, {"http://127.0.0.1:1", "k", ""}} {
		if Send(context.Background(), c[0], c[1], c[2], Event{}, day) == nil {
			t.Errorf("%v sent", c)
		}
	}
}
