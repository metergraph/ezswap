package providers

// JavaScript-exact helpers the client needs: String.prototype.trim, and
// JSON.stringify of a value JSON.parse read, key order included.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/jsnum"
)

// isJSSpace is a WhiteSpace or LineTerminator code point as trim() strips it.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// orderedObject is a JSON object as JSON.parse builds it: each key once, at
// the place it first appeared, holding the last value given for it.
type orderedObject struct {
	keys   []string
	values map[string]any
}

// decodeOrdered reads one JSON value, keeping object key order.
func decodeOrdered(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			object := &orderedObject{values: map[string]any{}}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("object key is not a string")
				}
				value, err := decodeOrdered(decoder)
				if err != nil {
					return nil, err
				}
				if _, seen := object.values[key]; !seen {
					object.keys = append(object.keys, key)
				}
				object.values[key] = value
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := decodeOrdered(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return t, nil
	}
}

// orderedArguments is the first tool call's arguments object, read from the
// reply's bytes and written as JSON.stringify writes it.
func orderedArguments(raw []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))))
	value, err := decodeOrdered(decoder)
	if err != nil {
		return "", false
	}
	field := func(value any, key string) any {
		if object, ok := value.(*orderedObject); ok {
			return object.values[key]
		}
		return nil
	}
	index := func(value any, i int) any {
		if list, ok := value.([]any); ok && len(list) > i {
			return list[i]
		}
		return nil
	}
	args := field(field(index(field(field(index(field(value, "choices"), 0), "message"), "tool_calls"), 0), "function"), "arguments")
	if _, ok := args.(*orderedObject); !ok {
		return "", false
	}
	return jsonStringify(args), true
}

// isArrayIndex is a key JavaScript orders first, as an integer index.
func isArrayIndex(key string) (uint64, bool) {
	n, err := strconv.ParseUint(key, 10, 32)
	if err != nil || n == math.MaxUint32 || strconv.FormatUint(n, 10) != key {
		return 0, false
	}
	return n, true
}

// jsKeyOrder is the order a JavaScript object enumerates its keys: integer
// indexes ascending, then the rest as given.
func jsKeyOrder(keys []string) []string {
	var indexes, names []string
	for _, key := range keys {
		if _, ok := isArrayIndex(key); ok {
			indexes = append(indexes, key)
		} else {
			names = append(names, key)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, _ := isArrayIndex(indexes[i])
		b, _ := isArrayIndex(indexes[j])
		return a < b
	})
	return append(indexes, names...)
}

// jsonStringify is JSON.stringify for a decoded JSON value. A Go map's keys
// come out sorted, having no order of their own.
func jsonStringify(value any) string {
	var b strings.Builder
	writeJSON(&b, value)
	return b.String()
}

func writeJSON(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(jsnum.String(v))
		}
	case json.Number:
		f, _ := v.Float64()
		writeJSON(b, f)
	case string:
		writeJSONString(b, v)
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, item)
		}
		b.WriteByte(']')
	case *orderedObject:
		writeObject(b, jsKeyOrder(v.keys), v.values)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		writeObject(b, jsKeyOrder(keys), v)
	default:
		data, _ := json.Marshal(v)
		b.Write(data)
	}
}

func writeObject(b *strings.Builder, keys []string, values map[string]any) {
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(b, key)
		b.WriteByte(':')
		writeJSON(b, values[key])
	}
	b.WriteByte('}')
}

func writeJSONString(b *strings.Builder, s string) {
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
