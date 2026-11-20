package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, ConfigFile)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func inDir(dir string) func(*IO) { return func(o *IO) { o.Cwd = dir } }

func TestSettingsFileStandsInForFlags(t *testing.T) {
	file := writePartTraces(t, partSpec{name: "classify", count: 12})
	dir := t.TempDir()
	writeConfig(t, dir, "models:\n  - google/gemini-2.5-flash-lite\nper-part: 12\nbudget: 2.5\nsample: 7\n")
	out := cli(t, args("estimate", file, "--map"), inDir(dir), nil)
	if out.code != ExitOK {
		t.Fatalf("%d %s", out.code, out.stderr)
	}
	// sample is for run and estimate without --map: left for them, not refused.
	match(t, `Settings from \S*replay\.yaml: budget, models, per-part\.`, out.stdout)
	match(t, `12 cases per part`, out.stdout)
	match(t, `Budget \$2\.50`, out.stdout)

	// A flag wins over the file.
	flagged := cli(t, args("estimate", file, "--map", "--per-part", "10"), inDir(dir), nil)
	match(t, `Settings from \S*replay\.yaml: budget, models\.`, flagged.stdout)
	match(t, `10 cases per part`, flagged.stdout)

	// --no-config ignores it, so --models is needed again.
	if bare := cli(t, args("estimate", file, "--map", "--no-config"), inDir(dir), nil); bare.code != ExitUsage || strings.Contains(bare.stdout, "Settings from") {
		t.Errorf("--no-config: %d %s %s", bare.code, bare.stdout, bare.stderr)
	}
}

func TestSettingsFileByPathAndForMapRun(t *testing.T) {
	file := twoParts(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "elsewhere.yaml")
	os.WriteFile(path, []byte("models: google/gemini-2.5-flash-lite\nignore: [\"$.n\"]\nhtml: page.html\nout: "+filepath.Join(dir, "out")+"\n"), 0o644)
	out := cli(t, args("map", file, "--yes", "--config", path), inDir(t.TempDir()), nil)
	if out.code != ExitOK {
		t.Fatalf("%d %s", out.code, out.stderr)
	}
	match(t, `Settings from \S*elsewhere\.yaml: html, ignore, models, out\.`, out.stdout)
	match(t, `classify +within noise`, out.stdout)
	if _, err := os.Stat(filepath.Join(dir, "out", "run.json")); err != nil {
		t.Errorf("out from the file was not used: %v", err)
	}
}

func TestSettingsFileRefusals(t *testing.T) {
	file := writePartTraces(t, partSpec{name: "classify", count: 12})
	for text, want := range map[string]string{
		"modles: x\n":                `"modles" is not a setting`,
		"yes: true\n":                `yes cannot be set in a settings file; confirm spending on the command line`,
		"json: true\n":               `json cannot be set`,
		"judge: please\n":            `judge takes true or false`,
		"per-part: [1, 2]\n":         `per-part takes a single value`,
		"models: [a, 3]\n":           `models takes a list of text`,
		"models: [unclosed\n":        `is not valid YAML`,
		"upstream: https://x.test\n": `record takes no settings file`,
	} {
		dir := t.TempDir()
		writeConfig(t, dir, text)
		out := cli(t, args("estimate", file, "--map", "--models", "google/gemini-2.5-flash-lite"), inDir(dir), nil)
		if out.code != ExitUsage || !strings.Contains(out.stderr, want) {
			t.Errorf("%q: %d, want %q in %s", text, out.code, want, out.stderr)
		}
	}
	// Other commands neither read the file nor take --config.
	dir := t.TempDir()
	writeConfig(t, dir, "modles: x\n")
	if out := cli(t, args("demo", "--map"), inDir(dir), nil); out.code != ExitOK {
		t.Errorf("demo read the file: %d %s", out.code, out.stderr)
	}
	if out := cli(t, args("demo", "--config", "x.yaml"), nil, nil); out.code != ExitUsage || !strings.Contains(out.stderr, "--config and --no-config are for estimate, map and run") {
		t.Errorf("demo --config: %d %s", out.code, out.stderr)
	}
	if out := cli(t, args("estimate", file, "--config", "missing.yaml"), inDir(dir), nil); out.code != ExitUsage || !strings.Contains(out.stderr, "Could not read") {
		t.Errorf("missing file: %d %s", out.code, out.stderr)
	}
}

// Every key a command takes from the file must be one its option check
// accepts, or a shared file would break the command it was not written for.
func TestSettingsKeysPassEachCommandsCheck(t *testing.T) {
	for _, c := range []struct {
		command string
		mapMode bool
	}{{"estimate", false}, {"estimate", true}, {"map", true}, {"run", false}} {
		for _, key := range configKeys(c.command, c.mapMode) {
			v := values{strings: map[string]string{}, lists: map[string][]string{}, bools: map[string]bool{}}
			switch options[key] {
			case boolOption:
				v.bools[key] = true
			case listOption:
				v.lists[key] = []string{"x"}
			default:
				v.strings[key] = "x"
			}
			if c.mapMode && c.command == "estimate" {
				v.bools["map"] = true
			}
			s := &session{v: v}
			if err := s.checkOptions(c.command, c.mapMode); err != nil {
				t.Errorf("%s (map %v) refuses %s from the file: %v", c.command, c.mapMode, key, err)
			}
		}
	}
}

func TestJudgeFromSettingsFileNamesTheFile(t *testing.T) {
	file := freeTextTraces(t)
	dir := t.TempDir()
	writeConfig(t, dir, "models: google/gemini-2.5-flash-lite\njudge: true\n")
	out := cli(t, args("map", file, "--yes"), inDir(dir), nil)
	if out.code != ExitRefused || !strings.Contains(out.stderr, "Set it, or set judge: false in "+ConfigFile+".") {
		t.Errorf("%d %s", out.code, out.stderr)
	}
	// From a flag, the advice is about the flag.
	flagged := cli(t, args("map", file, "--yes", "--judge", "--no-config", models), nil, nil)
	if !strings.Contains(flagged.stderr, "Set it, or leave out --judge.") {
		t.Errorf("%s", flagged.stderr)
	}
}
