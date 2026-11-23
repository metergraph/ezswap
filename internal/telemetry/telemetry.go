// Package telemetry is ezswap's anonymous usage report. It is off until a
// person turns it on: the guided workflow asks once, and `ezswap telemetry
// on` or `off` changes the answer. With it on, each command sends one event
// when it ends.
//
// An event holds only what Event declares: the command, how it ended,
// counts, and the names of models the public price catalog knows. It never
// holds a prompt, a model's reply, a key, a file path, a base URL, a part's
// name or an error's text. docs/telemetry.md lists every field.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Key is the project key events are sent with. It can write events and
// read nothing, so it is safe in source. Empty means this build has nowhere
// to send: ezswap then never asks and never sends.
var Key = ""

// DefaultURL is where events are sent.
const DefaultURL = "https://us.i.posthog.com/i/v0/e/"

// Timeout is the longest a command waits for its event to be sent.
const Timeout = 2 * time.Second

// Other is the name reported for a model the price catalog does not know,
// such as a private deployment or a fine-tune.
const Other = "other"

// File is the settings file's name inside ezswap's settings directory.
const File = "telemetry.json"

// Settings is the person's answer, as kept on disk.
type Settings struct {
	Enabled bool `json:"enabled"`
	// A random id made when telemetry is turned on, so one install's events
	// can be counted together. It is derived from nothing on the machine,
	// and turning telemetry off deletes it.
	ID string `json:"id,omitempty"`
	// The day the answer was given (YYYY-MM-DD).
	Answered string `json:"answered"`
}

// Load reads the settings in dir. answered is false when the person has not
// been asked, or the file cannot be read: both mean nothing is sent.
func Load(dir string) (s Settings, answered bool) {
	if dir == "" {
		return Settings{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		return Settings{}, false
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, false
	}
	if s.Enabled && s.ID == "" {
		s.Enabled = false
	}
	return s, true
}

// Answer records the person's answer in dir and returns what was written.
// Turning telemetry on keeps an id already there; turning it off drops it.
func Answer(dir string, enabled bool, now time.Time) (Settings, error) {
	if dir == "" {
		return Settings{}, errors.New("no settings directory on this machine")
	}
	previous, _ := Load(dir)
	s := Settings{Enabled: enabled, Answered: now.UTC().Format("2006-01-02")}
	if enabled {
		s.ID = previous.ID
		if s.ID == "" {
			id, err := newID()
			if err != nil {
				return Settings{}, err
			}
			s.ID = id
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Settings{}, err
	}
	return s, os.WriteFile(filepath.Join(dir, File), append(data, '\n'), 0o600)
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// OffBy is the environment variable that turns telemetry off whatever the
// settings say, or "". DO_NOT_TRACK and EZSWAP_TELEMETRY=0 are a person's
// choice; CI means nobody is there to have made one.
func OffBy(env map[string]string) string {
	if v := strings.TrimSpace(env["DO_NOT_TRACK"]); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return "DO_NOT_TRACK"
	}
	switch strings.ToLower(strings.TrimSpace(env["EZSWAP_TELEMETRY"])) {
	case "0", "off", "false", "no":
		return "EZSWAP_TELEMETRY"
	}
	if env["CI"] != "" {
		return "CI"
	}
	return ""
}

// Event is everything one command reports. A field left at its zero value
// is not sent.
type Event struct {
	// The command: guided, demo, estimate, map, run, record, report or judge.
	Command string
	Version string
	OS      string
	Arch    string
	// The exit code, and the error's code (such as "over_budget") if any.
	ExitCode int
	Error    string
	// Whole seconds the command took.
	Seconds int
	// Whether it ran on an interactive terminal.
	Interactive bool
	// The guided workflow: where it started (record, analyze or open) and
	// the objective chosen (cost, quality or latest).
	Start     string
	Objective string
	// The models compared and the model the traces were recorded on, each
	// by its catalog name, or Other.
	Models     []string
	Production string
	// Counts: the app's parts in the map, and the cases replayed.
	Parts int
	Cases int
	// Whether the judge was on.
	Judge bool
}

// Properties is the event as sent.
func (e Event) Properties() map[string]any {
	p := map[string]any{
		"command":     e.Command,
		"version":     e.Version,
		"os":          e.OS,
		"arch":        e.Arch,
		"exit_code":   e.ExitCode,
		"seconds":     e.Seconds,
		"interactive": e.Interactive,
	}
	text := map[string]string{"error": e.Error, "start": e.Start, "objective": e.Objective, "production": e.Production}
	for k, v := range text {
		if v != "" {
			p[k] = v
		}
	}
	if len(e.Models) > 0 {
		p["models"] = e.Models
	}
	if e.Parts > 0 {
		p["parts"] = e.Parts
	}
	if e.Cases > 0 {
		p["cases"] = e.Cases
	}
	if e.Judge {
		p["judge"] = true
	}
	return p
}

// Name is the event's name.
const Name = "ezswap command"

// Body is the request body for an event from the install id.
func Body(key, id string, e Event, now time.Time) ([]byte, error) {
	p := e.Properties()
	// No person profile, and no location worked out from the address.
	p["$process_person_profile"] = false
	p["$geoip_disable"] = true
	return json.Marshal(map[string]any{
		"api_key":     key,
		"event":       Name,
		"distinct_id": id,
		"timestamp":   now.UTC().Format(time.RFC3339),
		"properties":  p,
	})
}

// Send posts one event and waits at most Timeout. A failure is returned for
// tests; the CLI drops it, since a report that cannot be sent is not the
// person's problem.
func Send(ctx context.Context, url, key, id string, e Event, now time.Time) error {
	if url == "" || key == "" || id == "" {
		return errors.New("telemetry: nowhere to send")
	}
	body, err := Body(key, id, e, now)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ezswap/"+e.Version)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return errors.New("telemetry: " + res.Status)
	}
	return nil
}
