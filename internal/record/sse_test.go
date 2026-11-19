package record

import (
	"reflect"
	"strings"
	"testing"
)

type sseEvent struct{ Event, Data string }

func parseSSE(pieces ...string) []sseEvent {
	var events []sseEvent
	parser := &sseParser{emit: func(event string, data []byte) {
		events = append(events, sseEvent{event, string(data)})
	}}
	for _, piece := range pieces {
		parser.feed([]byte(piece))
	}
	return events
}

func TestSSEParsing(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		want   []sseEvent
	}{
		{"data only", "data: one\n\ndata: two\n\n", []sseEvent{{"", "one"}, {"", "two"}}},
		{"event name", "event: message_start\ndata: {\"a\":1}\n\n", []sseEvent{{"message_start", `{"a":1}`}}},
		{"name does not carry over", "event: first\ndata: 1\n\ndata: 2\n\n", []sseEvent{{"first", "1"}, {"", "2"}}},
		{"CRLF", "event: e\r\ndata: one\r\n\r\ndata: two\r\n\r\n", []sseEvent{{"e", "one"}, {"", "two"}}},
		{"lone CR", "data: one\r\rdata: two\r\r", []sseEvent{{"", "one"}, {"", "two"}}},
		{"multi-line data", "data: {\"a\":\ndata: 1}\n\n", []sseEvent{{"", "{\"a\":\n1}"}}},
		{"comments", ": keep-alive\n\n:\ndata: one\n: between\ndata: two\n\n", []sseEvent{{"", "one\ntwo"}}},
		{"no space after the colon", "data:one\nevent:e\n\n", []sseEvent{{"e", "one"}}},
		{"only one space is syntax", "data:  two spaces\n\n", []sseEvent{{"", " two spaces"}}},
		{"empty data", "data\n\ndata:\n\n", []sseEvent{{"", ""}, {"", ""}}},
		{"other fields", "id: 7\nretry: 100\ndata: one\n\n", []sseEvent{{"", "one"}}},
		{"event without data", "event: ping\n\ndata: one\n\n", []sseEvent{{"", "one"}}},
		{"byte order mark", "\xEF\xBB\xBFdata: one\n\n", []sseEvent{{"", "one"}}},
		{"unfinished event", "data: one\n\ndata: two\n", []sseEvent{{"", "one"}}},
		{"unfinished line", "data: one\n\ndata: tw", []sseEvent{{"", "one"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseSSE(test.stream); !reflect.DeepEqual(got, test.want) {
				t.Errorf("whole: got %+v, want %+v", got, test.want)
			}
			// The same events however the stream is split across reads.
			if got := parseSSE(strings.Split(test.stream, "")...); !reflect.DeepEqual(got, test.want) {
				t.Errorf("byte by byte: got %+v, want %+v", got, test.want)
			}
			for cut := range len(test.stream) {
				if got := parseSSE(test.stream[:cut], test.stream[cut:]); !reflect.DeepEqual(got, test.want) {
					t.Errorf("split at %d: got %+v, want %+v", cut, got, test.want)
				}
			}
		})
	}
}
