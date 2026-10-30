// Package adapters holds the trace file readers: a JSONL file (one record per
// line), a JSON array, or a single JSON document such as an OTLP export,
// read into []CapturedCall.
package adapters

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/types"
)

// Adapters in detection order. Detection order matters: the most specific
// shapes first.
var Adapters = []Adapter{Otel, Langfuse, ReplayJSONL, Raw}

func adapterNames() string {
	names := make([]string, len(Adapters))
	for i, a := range Adapters {
		names[i] = a.Name()
	}
	return strings.Join(names, ", ")
}

// GetAdapter is the adapter with this name.
func GetAdapter(name string) (Adapter, error) {
	for _, candidate := range Adapters {
		if candidate.Name() == name {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("Unknown trace format %q. Use one of: %s.", name, adapterNames())
}

// DetectAdapter is the adapter whose format the first record of a file looks
// like, or nil.
func DetectAdapter(firstRecord any) Adapter {
	for _, candidate := range Adapters {
		if candidate.Detect(firstRecord) {
			return candidate
		}
	}
	return nil
}

// ReadTracesOptions tune ReadTraces and IterateTraces.
type ReadTracesOptions struct {
	// Adapter name; detected from the first record when empty.
	Adapter string
	// Stop after this many calls; nil reads them all.
	Limit *int
	// Called for each line that is not JSON or that the adapter could not read.
	OnSkip func(line int, reason string)
}

type sourceRecord struct {
	record  any
	context RecordContext
}

// firstChar is the file's first character that is not JavaScript whitespace
// (a byte order mark counts as whitespace), or "" for a blank file.
func firstChar(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		r, _, err := reader.ReadRune()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if !jsval.IsSpace(r) {
			return string(r), nil
		}
	}
}

func stripBOM(text string) string {
	return strings.TrimPrefix(text, "\uFEFF")
}

func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return stripBOM(jsval.DecodeUTF8(data)), nil
}

func wholeDocument(path string, yield func(sourceRecord) bool) error {
	text, err := readText(path)
	if err != nil {
		return err
	}
	document, err := jsval.Parse(text)
	if err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if list, ok := document.([]any); ok {
		for i, item := range list {
			if !yield(sourceRecord{item, RecordContext{Line: i + 1, Raw: jsval.Stringify(item)}}) {
				return nil
			}
		}
		return nil
	}
	yield(sourceRecord{document, RecordContext{Line: 1, Raw: text}})
	return nil
}

// splitLines splits on \n, \r\n, a lone \r, U+2028 and U+2029, as Node's
// readline does.
func splitLines(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			return 0, nil, nil // a \r at the end of the buffer: is a \n next?
		case 0xE2:
			// U+2028 and U+2029 are E2 80 A8 and E2 80 A9.
			if i+2 < len(data) {
				if data[i+1] == 0x80 && (data[i+2] == 0xA8 || data[i+2] == 0xA9) {
					return i + 3, data[:i], nil
				}
			} else if !atEOF {
				return 0, nil, nil // the sequence may continue in the next read
			}
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// maxLine bounds one line in memory; Node's readline has no limit short of
// the string size cap.
const maxLine = 1 << 30

// records are the records of a file. A file that starts with `[` is one JSON
// array. Any other is read line by line; if its first line is not JSON on
// its own, the file is one pretty-printed document (an OTLP export, say).
func records(path string, onSkip func(int, string), yield func(sourceRecord) bool) error {
	start, err := firstChar(path)
	if err != nil {
		return err
	}
	if start == "" {
		return nil
	}
	if start == "[" {
		return wholeDocument(path, yield)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	scanner.Split(splitLines)
	line := 0
	seenRecord := false
	for scanner.Scan() {
		line++
		text := scanner.Bytes()
		var decoded string
		if utf8.Valid(text) {
			decoded = string(text)
		} else {
			decoded = jsval.DecodeUTF8(text)
		}
		trimmed := jsval.Trim(stripBOM(decoded))
		if trimmed == "" {
			continue
		}
		record, err := jsval.Parse(trimmed)
		if err != nil {
			if !seenRecord {
				file.Close()
				return wholeDocument(path, yield)
			}
			if onSkip != nil {
				onSkip(line, "not valid JSON")
			}
			continue
		}
		seenRecord = true
		if !yield(sourceRecord{record, RecordContext{Line: line, Raw: trimmed}}) {
			return nil
		}
	}
	return scanner.Err()
}

// readSafely is adapter.Read with a panic reported as an error, so one bad
// record cannot take down the read.
func readSafely(a Adapter, record any, context RecordContext) (calls []types.CapturedCall, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = errors.New(fmt.Sprint(r))
			}
		}
	}()
	return a.Read(record, context), nil
}

// IterateTraces streams calls from a trace file. A failure ends the sequence
// with its error.
func IterateTraces(path string, options ReadTracesOptions) iter.Seq2[types.CapturedCall, error] {
	return func(yield func(types.CapturedCall, error) bool) {
		var adapter Adapter
		if options.Adapter != "" {
			a, err := GetAdapter(options.Adapter)
			if err != nil {
				yield(types.CapturedCall{}, err)
				return
			}
			adapter = a
		}
		if options.Limit != nil && *options.Limit <= 0 {
			return
		}
		count := 0
		stopped := false
		var failure error
		err := records(path, options.OnSkip, func(source sourceRecord) bool {
			if adapter == nil {
				adapter = DetectAdapter(source.record)
				if adapter == nil {
					failure = fmt.Errorf(
						"Could not tell the trace format from the first record of %s. Pass the format explicitly: %s.",
						path, adapterNames())
					return false
				}
			}
			calls, err := readSafely(adapter, source.record, source.context)
			if err != nil {
				if options.OnSkip != nil {
					options.OnSkip(source.context.Line, err.Error())
				}
				return true
			}
			for _, captured := range calls {
				if !yield(captured, nil) {
					stopped = true
					return false
				}
				count++
				if options.Limit != nil && count >= *options.Limit {
					stopped = true
					return false
				}
			}
			return true
		})
		if stopped {
			return
		}
		if failure == nil {
			failure = err
		}
		if failure != nil {
			yield(types.CapturedCall{}, failure)
		}
	}
}

// ReadTraces reads every call (up to Limit) from a trace file.
func ReadTraces(path string, options ReadTracesOptions) ([]types.CapturedCall, error) {
	calls := []types.CapturedCall{}
	for captured, err := range IterateTraces(path, options) {
		if err != nil {
			return nil, err
		}
		calls = append(calls, captured)
	}
	return calls, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
