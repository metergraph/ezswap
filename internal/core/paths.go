package core

// Tracked-field paths: a small JSONPath subset (`$.a.b[0]`, `a.b`,
// `$["a key"]`) with no wildcards, filters or recursion, because a tracked
// field names one value per output. Two readers: ParseJSONPath/extract,
// which the summary and noise floor use (Python regex rules), and
// ParsePath/ValueAt, which the field comparisons use (JavaScript rules) and
// which also takes `[]` for every item of a list.
//
// Both grammars need lookahead and back-references, which RE2 lacks, so each
// is a small scanner
// here that accepts exactly what its expression accepts.

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PathToken is one step of a tracked field path: a key, or an index when
// IsIndex is set. An index is kept as JavaScript's Number.parseInt reads it.
type PathToken struct {
	Key     string
	Index   float64
	IsIndex bool
}

func isPathStart(r rune) bool {
	return r == '_' || r == '$' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// Python's `\w` is Unicode-aware, hence the property classes after the first character.
func isPathRest(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '$' || r == '-'
}

// pathToken reads one token at the start of text, or reports none.
func pathToken(text string) (PathToken, int, bool) {
	if text == "" {
		return PathToken{}, 0, false
	}
	if text[0] == '.' {
		r, size := utf8.DecodeRuneInString(text[1:])
		if !isPathStart(r) {
			return PathToken{}, 0, false
		}
		end := 1 + size
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if !isPathRest(r) {
				break
			}
			end += size
		}
		return PathToken{Key: text[1:end]}, end, true
	}
	if text[0] != '[' {
		return PathToken{}, 0, false
	}
	// \[(-?[0-9]+)\]
	i := 1
	if i < len(text) && text[i] == '-' {
		i++
	}
	digits := i
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i > digits && i < len(text) && text[i] == ']' {
		n, _ := strconv.ParseFloat(text[1:i], 64)
		return PathToken{Index: n, IsIndex: true}, i + 1, true
	}
	// \[(["'])((?:(?!\1)[^\n])*)\1\]
	if len(text) > 1 && (text[1] == '"' || text[1] == '\'') {
		quote := text[1]
		j := 2
		for j < len(text) && text[j] != quote && text[j] != '\n' {
			j++
		}
		if j+1 < len(text) && text[j] == quote && text[j+1] == ']' {
			return PathToken{Key: text[2:j]}, j + 2, true
		}
	}
	return PathToken{}, 0, false
}

// ParseJSONPath reads a tracked field path as keys and indexes. It refuses a
// path it cannot read.
func ParseJSONPath(path string) ([]PathToken, error) {
	text := jsTrim(path)
	if strings.HasPrefix(text, "$") {
		text = text[1:]
	} else if text != "" && !strings.HasPrefix(text, ".") && !strings.HasPrefix(text, "[") {
		text = "." + text
	}
	var tokens []PathToken
	for position := 0; position < len(text); {
		token, n, ok := pathToken(text[position:])
		if !ok {
			return nil, errors.New(jsonQuote(path) + " is not a supported JSON path (use $.field.child[0])")
		}
		tokens = append(tokens, token)
		position += n
	}
	if len(tokens) == 0 {
		return nil, errors.New(jsonQuote(path) + " names no field")
	}
	return tokens, nil
}

// mustParseJSONPath is ParseJSONPath for a path known to read; any other
// path panics.
func mustParseJSONPath(path string) []PathToken {
	tokens, err := ParseJSONPath(path)
	if err != nil {
		panic(err)
	}
	return tokens
}

// extract is the value at a path, and whether it is there: a missing path is
// not the same as a null value.
func extract(value any, path []PathToken) (any, bool) {
	current := value
	for _, token := range path {
		if token.IsIndex {
			items, ok := current.([]any)
			n := float64(len(items))
			if !ok || !(-n <= token.Index && token.Index < n) {
				return nil, false
			}
			i := int(token.Index)
			if token.Index < 0 {
				i = len(items) + i
			}
			current = items[i]
		} else {
			if !isPlainObject(current) || !has(current, token.Key) {
				return nil, false
			}
			current, _ = get(current, token.Key)
		}
	}
	return current, true
}

// --- The field comparisons' reader -------------------------------------------

// PageToken is one step of a field comparison path: a key, an index, or
// every item of a list.
type PageToken struct {
	Key   string
	Index float64
	Kind  PageTokenKind
}

// PageTokenKind says which a PageToken is.
type PageTokenKind int

const (
	TokenKey PageTokenKind = iota
	TokenIndex
	TokenEach
)

func isWordStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isWordRest(c byte) bool { return isWordStart(c) || (c >= '0' && c <= '9') || c == '-' }

// isIdentifier is /^[A-Za-z_$][\w$-]*$/.
func isIdentifier(key string) bool {
	if key == "" || !isWordStart(key[0]) {
		return false
	}
	for i := 1; i < len(key); i++ {
		if !isWordRest(key[i]) {
			return false
		}
	}
	return true
}

// isLineTerminator is what JavaScript's `.` does not match.
func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// pageToken reads one of /\.([A-Za-z_$][\w$-]*)|\[(-?\d+)\]|\[(["'])((?:(?!\3).)*)\3\]|\[\]/y.
func pageToken(text string) (PageToken, int, bool) {
	if text == "" {
		return PageToken{}, 0, false
	}
	if text[0] == '.' {
		if len(text) < 2 || !isWordStart(text[1]) {
			return PageToken{}, 0, false
		}
		end := 2
		for end < len(text) && isWordRest(text[end]) {
			end++
		}
		return PageToken{Key: text[1:end]}, end, true
	}
	if text[0] != '[' {
		return PageToken{}, 0, false
	}
	i := 1
	if i < len(text) && text[i] == '-' {
		i++
	}
	digits := i
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i > digits && i < len(text) && text[i] == ']' {
		n, _ := strconv.ParseFloat(text[1:i], 64)
		return PageToken{Index: n, Kind: TokenIndex}, i + 1, true
	}
	if len(text) > 1 && (text[1] == '"' || text[1] == '\'') {
		quote := rune(text[1])
		j := 2
		for j < len(text) {
			r, size := utf8.DecodeRuneInString(text[j:])
			if r == quote || isLineTerminator(r) {
				break
			}
			j += size
		}
		if j+1 < len(text) && rune(text[j]) == quote && text[j+1] == ']' {
			return PageToken{Key: text[2:j]}, j + 2, true
		}
	}
	if strings.HasPrefix(text, "[]") {
		return PageToken{Kind: TokenEach}, 2, true
	}
	return PageToken{}, 0, false
}

func step(prefix, key string) string {
	if isIdentifier(key) {
		return prefix + "." + key
	}
	return prefix + "[" + jsonQuote(key) + "]"
}

// JSONPaths is jsonPaths: the leaf paths of a value. An array of objects is
// walked as `$.a[].b`.
func JSONPaths(value any, prefix string) []string {
	if isPlainObject(value) {
		var out []string
		for _, key := range keysOf(value) {
			v, _ := get(value, key)
			out = append(out, JSONPaths(v, step(prefix, key))...)
		}
		return out
	}
	if items, ok := value.([]any); ok {
		anyObject := false
		for _, item := range items {
			if isPlainObject(item) {
				anyObject = true
				break
			}
		}
		if anyObject {
			var keys []string
			seen := map[string]bool{}
			for _, item := range items {
				if isPlainObject(item) {
					for _, k := range keysOf(item) {
						if !seen[k] {
							seen[k] = true
							keys = append(keys, k)
						}
					}
				}
			}
			var out []string
			for _, key := range keys {
				var held any
				for _, item := range items {
					if isPlainObject(item) && has(item, key) {
						held, _ = get(item, key)
						break
					}
				}
				out = append(out, JSONPaths(held, step(prefix+"[]", key))...)
			}
			return out
		}
	}
	if prefix == "$" {
		return nil
	}
	return []string{prefix}
}

// ParsePath is parsePath: the field comparisons' tokens for a path, or false
// when it cannot read it.
func ParsePath(path string) ([]PageToken, bool) {
	text := jsTrim(path)
	if strings.HasPrefix(text, "$") {
		text = text[1:]
	} else if text != "" && text[0] != '.' && text[0] != '[' {
		text = "." + text
	}
	tokens := []PageToken{}
	for position := 0; position < len(text); {
		token, n, ok := pageToken(text[position:])
		if !ok {
			return nil, false
		}
		tokens = append(tokens, token)
		position += n
	}
	return tokens, true
}

// ValueAt is valueAt: the value at a path, every item's for a `[]` path, and
// whether there is one (JavaScript's undefined when not).
func ValueAt(value any, path string) (any, bool) {
	tokens, ok := ParsePath(path)
	if !ok {
		return nil, false
	}
	current := []any{value}
	listed := false
	for _, token := range tokens {
		var next []any
		for _, node := range current {
			switch token.Kind {
			case TokenEach:
				if items, ok := node.([]any); ok {
					next = append(next, items...)
				}
			case TokenIndex:
				items, ok := node.([]any)
				if !ok {
					continue
				}
				i := token.Index
				if i < 0 {
					i = float64(len(items)) + i
				}
				if i >= 0 && i < float64(len(items)) {
					next = append(next, items[int(i)])
				}
			default:
				if isPlainObject(node) && has(node, token.Key) {
					v, _ := get(node, token.Key)
					next = append(next, v)
				}
			}
		}
		current = next
		if token.Kind == TokenEach {
			listed = true
		}
	}
	if listed {
		if len(current) == 0 {
			return nil, false
		}
		return current, true
	}
	if len(current) == 0 {
		return nil, false
	}
	return current[0], true
}
