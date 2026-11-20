// Command build-prices converts metergraph-core's public price catalog
// (prices.yaml, Apache-2.0) to internal/pricing/prices.json, the catalog this
// tool embeds.
//
//	go run ./cmd/build-prices <path to a clone of the metergraph repository>
//
// The header records the source repository, the file's path in it and the
// commit that last changed the file, so a report can say which prices it used.
// Rates are kept as decimal strings: the cost function does exact decimal
// arithmetic, as metergraph-core does.
//
// The output is byte for byte what the original JavaScript build script
// wrote: YAML 1.2 core-schema scalars with YAML 1.1 merge keys, numbers
// printed the way JavaScript prints them, and JSON.stringify's layout with a
// one-space indent. Keep it that way, so regenerating from the same commit
// leaves prices.json unchanged.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/metergraph/ezswap/internal/jsnum"
	"go.yaml.in/yaml/v3"
)

const (
	sourceRepo = "https://github.com/metergraph/metergraph"
	sourcePath = "core/src/metergraph_core/data/prices.yaml"
)

var rateFields = []string{
	"input_per_mtok",
	"output_per_mtok",
	"cache_read_per_mtok",
	"cache_write_5m_per_mtok",
	"cache_write_1h_per_mtok",
	"batch_input_per_mtok",
	"batch_output_per_mtok",
}

func main() {
	out := flag.String("out", "internal/pricing/prices.json", "where to write the catalog")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/build-prices [-out path] <metergraph clone>")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	n, commit, err := build(flag.Arg(0), *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d models from %s to %s\n", n, commit[:12], *out)
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func git(clone string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", clone}, args...)...)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}

func build(clone, out string) (int, string, error) {
	commit, err := git(clone, "log", "-1", "--format=%H", "--", sourcePath)
	if err != nil {
		return 0, "", err
	}
	if !commitPattern.MatchString(commit) {
		return 0, "", fmt.Errorf("no commit found for %s in the clone", sourcePath)
	}
	status, err := git(clone, "status", "--porcelain", "--", sourcePath)
	if err != nil {
		return 0, "", err
	}
	if status != "" {
		return 0, "", fmt.Errorf("%s has uncommitted changes in the clone; build from a committed file", sourcePath)
	}
	text, err := os.ReadFile(filepath.Join(clone, filepath.FromSlash(sourcePath)))
	if err != nil {
		return 0, "", err
	}
	data, n, err := render(text, commit)
	if err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return 0, "", err
	}
	return n, commit, nil
}

// render converts prices.yaml to the prices.json bytes and the model count.
func render(text []byte, commit string) ([]byte, int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(text, &root); err != nil {
		return nil, 0, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, 0, errors.New("prices.yaml is empty")
	}
	doc, err := toValue(root.Content[0])
	if err != nil {
		return nil, 0, err
	}
	output, n, err := convert(doc, commit)
	if err != nil {
		return nil, 0, err
	}
	var b bytes.Buffer
	writeJSON(&b, output, "")
	b.WriteByte('\n')
	return b.Bytes(), n, nil
}

// Values are what the JavaScript YAML reader produced: nil (null), bool,
// float64, string, *object and []any. undefined is a key that is not there.

type undefinedType struct{}

var undefined = undefinedType{}

// object is a JavaScript object: keys in insertion order.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) set(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
}

func (o *object) setIfAbsent(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.set(key, value)
	}
}

func (o *object) get(key string) any {
	if v, ok := o.vals[key]; ok {
		return v
	}
	return undefined
}

// ownKeys is JavaScript's property order: array-index keys ascending, then
// the rest in insertion order.
func (o *object) ownKeys() []string {
	var index, rest []string
	for _, k := range o.keys {
		if isArrayIndex(k) {
			index = append(index, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(index, func(i, j int) bool {
		a, _ := strconv.ParseUint(index[i], 10, 64)
		b, _ := strconv.ParseUint(index[j], 10, 64)
		return a < b
	})
	return append(index, rest...)
}

func isArrayIndex(k string) bool {
	if k == "" || (len(k) > 1 && k[0] == '0') {
		return false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < math.MaxUint32
}

// YAML 1.2 core schema, as the JavaScript yaml package resolves plain scalars.
var (
	nullPattern  = regexp.MustCompile(`^(?:~|[Nn]ull|NULL)?$`)
	truePattern  = regexp.MustCompile(`^(?:[Tt]rue|TRUE)$`)
	falsePattern = regexp.MustCompile(`^(?:[Ff]alse|FALSE)$`)
	octPattern   = regexp.MustCompile(`^0o[0-7]+$`)
	intPattern   = regexp.MustCompile(`^[-+]?[0-9]+$`)
	hexPattern   = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	infPattern   = regexp.MustCompile(`^[-+]?\.(?:inf|Inf|INF)$`)
	nanPattern   = regexp.MustCompile(`^\.(?:nan|NaN|NAN)$`)
	expPattern   = regexp.MustCompile(`^[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)[eE][-+]?[0-9]+$`)
	floatPattern = regexp.MustCompile(`^[-+]?(?:\.[0-9]+|[0-9]+\.[0-9]*)$`)
)

func scalar(n *yaml.Node) (any, error) {
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return n.Value, nil
	}
	if n.Tag == "!!str" && n.Style&yaml.TaggedStyle != 0 {
		return n.Value, nil
	}
	v := n.Value
	switch {
	case nullPattern.MatchString(v):
		return nil, nil
	case truePattern.MatchString(v):
		return true, nil
	case falsePattern.MatchString(v):
		return false, nil
	case octPattern.MatchString(v):
		i, err := strconv.ParseUint(v[2:], 8, 64)
		return float64(i), err
	case intPattern.MatchString(v), expPattern.MatchString(v), floatPattern.MatchString(v):
		return strconv.ParseFloat(v, 64)
	case hexPattern.MatchString(v):
		i, err := strconv.ParseUint(v[2:], 16, 64)
		return float64(i), err
	case infPattern.MatchString(v):
		if v[0] == '-' {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case nanPattern.MatchString(v):
		return math.NaN(), nil
	}
	return v, nil
}

func toValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return toValue(n.Alias)
	case yaml.ScalarNode:
		return scalar(n)
	case yaml.SequenceNode:
		items := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toValue(c)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return items, nil
	case yaml.MappingNode:
		o := newObject()
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.ScalarNode && k.Value == "<<" && k.Style&^yaml.TaggedStyle == 0 {
				if err := merge(o, v); err != nil {
					return nil, err
				}
				continue
			}
			key, err := toValue(k)
			if err != nil {
				return nil, err
			}
			value, err := toValue(v)
			if err != nil {
				return nil, err
			}
			o.set(jsString(key), value)
		}
		return o, nil
	}
	return nil, fmt.Errorf("line %d: unexpected YAML node", n.Line)
}

// merge applies a YAML 1.1 merge key: each source map's keys are added unless
// the map already has them, earlier sources first.
func merge(o *object, n *yaml.Node) error {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	sources := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		sources = n.Content
	}
	for _, s := range sources {
		for s.Kind == yaml.AliasNode {
			s = s.Alias
		}
		if s.Kind != yaml.MappingNode {
			return fmt.Errorf("line %d: merge sources must be maps or map aliases", s.Line)
		}
		v, err := toValue(s)
		if err != nil {
			return err
		}
		src := v.(*object)
		for _, k := range src.keys {
			o.setIfAbsent(k, src.vals[k])
		}
	}
	return nil
}

// jsString is JavaScript's String(value).
func jsString(v any) string {
	switch v := v.(type) {
	case undefinedType:
		return "undefined"
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return jsnum.String(v)
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item == nil || item == undefined {
				continue
			}
			parts[i] = jsString(item)
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func truthy(v any) bool {
	switch v := v.(type) {
	case undefinedType, nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	}
	return true
}

func missing(v any) bool { return v == nil || v == undefined }

// text is a date or other scalar as a string, or null.
func text(v any) any {
	if missing(v) {
		return nil
	}
	return jsString(v)
}

func rate(v any) (any, error) {
	switch v.(type) {
	case undefinedType, nil:
		return nil, nil
	case float64, string:
		return jsString(v), nil
	}
	var b bytes.Buffer
	writeJSON(&b, v, "")
	return nil, fmt.Errorf("unexpected rate %s", b.String())
}

func orNull(v any) any {
	if v == undefined {
		return nil
	}
	return v
}

// compact drops the keys whose value is null.
func compact(o *object) *object {
	out := newObject()
	for _, k := range o.keys {
		if !missing(o.vals[k]) {
			out.set(k, o.vals[k])
		}
	}
	return out
}

func list(v any, what string) ([]any, error) {
	if missing(v) {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a list", what)
	}
	return items, nil
}

func entryObject(v any, what string) (*object, error) {
	o, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("%s is not a map", what)
	}
	return o, nil
}

func convert(doc any, commit string) (*object, int, error) {
	document, err := entryObject(doc, "the document")
	if err != nil {
		return nil, 0, err
	}
	entries, ok := document.get("models").([]any)
	if !ok {
		return nil, 0, errors.New("models is not a list")
	}
	models := make([]any, 0, len(entries))
	for _, e := range entries {
		entry, err := entryObject(e, "a model")
		if err != nil {
			return nil, 0, err
		}
		model := newObject()
		model.set("canonical_id", jsString(entry.get("canonical_id")))
		if p := entry.get("publisher"); truthy(p) {
			model.set("publisher", strings.ToLower(strings.TrimFunc(jsString(p), isJSSpace)))
		} else {
			model.set("publisher", nil)
		}

		rawAliases, err := list(entry.get("aliases"), "aliases")
		if err != nil {
			return nil, 0, err
		}
		aliases := make([]any, 0, len(rawAliases))
		for _, a := range rawAliases {
			alias, err := entryObject(a, "an alias")
			if err != nil {
				return nil, 0, err
			}
			out := newObject()
			out.set("provider", jsString(alias.get("provider")))
			out.set("alias", jsString(alias.get("alias")))
			out.set("channel", jsString(alias.get("channel")))
			out.set("rules", orNull(alias.get("rules")))
			out.set("effective_from", text(alias.get("effective_from")))
			out.set("effective_to", text(alias.get("effective_to")))
			if region := alias.get("price_region"); missing(region) {
				out.set("price_region", nil)
			} else {
				out.set("price_region", regions(region))
			}
			aliases = append(aliases, compact(out))
		}
		model.set("aliases", aliases)

		rawPrices, err := list(entry.get("prices"), "prices")
		if err != nil {
			return nil, 0, err
		}
		prices := make([]any, 0, len(rawPrices))
		for _, p := range rawPrices {
			price, err := entryObject(p, "a price")
			if err != nil {
				return nil, 0, err
			}
			out := newObject()
			out.set("channel", jsString(price.get("channel")))
			if region := price.get("region"); truthy(region) {
				out.set("region", jsString(region))
			} else {
				out.set("region", "global")
			}
			out.set("effective_from", text(price.get("effective_from")))
			out.set("effective_to", text(price.get("effective_to")))
			for _, field := range rateFields {
				r, err := rate(price.get(field))
				if err != nil {
					return nil, 0, err
				}
				out.set(field, r)
			}
			out.set("rules", orNull(price.get("rules")))
			out.set("source_url", jsString(price.get("source_url")))
			prices = append(prices, compact(out))
		}
		model.set("prices", prices)
		models = append(models, model)
	}

	source := newObject()
	source.set("repository", sourceRepo)
	source.set("path", sourcePath)
	source.set("commit", commit)
	source.set("license", "Apache-2.0")
	output := newObject()
	output.set("source", source)
	output.set("version", jsString(document.get("version")))
	output.set("currency", jsString(document.get("currency")))
	output.set("pricing_verified_at", text(document.get("pricing_verified_at")))
	output.set("models", models)
	return output, len(models), nil
}

// regions is [value].flat().map(String).
func regions(v any) []any {
	items, ok := v.([]any)
	if !ok {
		items = []any{v}
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = jsString(item)
	}
	return out
}

// isJSSpace is what String.prototype.trim removes.
func isJSSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\uFEFF'
}

// writeJSON is JSON.stringify(v, null, 1).
func writeJSON(b *bytes.Buffer, v any, indent string) {
	switch v := v.(type) {
	case nil, undefinedType:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(jsnum.String(v))
		}
	case string:
		writeString(b, v)
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		inner := indent + " "
		b.WriteString("[\n")
		for i, item := range v {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			writeJSON(b, item, inner)
		}
		b.WriteString("\n" + indent + "]")
	case *object:
		var keys []string
		for _, k := range v.ownKeys() {
			if v.vals[k] != undefined {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			b.WriteString("{}")
			return
		}
		inner := indent + " "
		b.WriteString("{\n")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			writeString(b, k)
			b.WriteString(": ")
			writeJSON(b, v.vals[k], inner)
		}
		b.WriteString("\n" + indent + "}")
	}
}

func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
