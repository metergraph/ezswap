package record

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// result is what a finished request sends to the writer.
type result struct {
	// A request that was not a model call; nothing else is set.
	forwarded bool
	event     Event
	// The whole line, newline included, for a recorded call.
	line []byte
}

// writer owns the trace file and the summary. One goroutine runs it, so
// lines are appended whole, in the order calls finish, and Events is never
// called concurrently.
type writer struct {
	file    *os.File
	events  func(Event)
	summary Summary
	// The file does not end with a newline: write one before the next line.
	needNewline bool
	err         error // the first write failure
}

// openTraceFile opens the trace file for appending, creating it (0600) and
// its directory (0700) when they are new.
func openTraceFile(path string, events func(Event)) (*writer, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("could not create the output directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("could not open the trace file: %w", err)
	}
	w := &writer{
		file:    file,
		events:  events,
		summary: Summary{File: path, ByPart: map[string]int{}, Skipped: map[string]int{}},
	}
	// A file left without a final newline (an interrupted write, a hand
	// edit) would otherwise have the next call glued to its last line.
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("could not read the trace file: %w", err)
	}
	if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := file.ReadAt(last, info.Size()-1); err != nil && err != io.EOF {
			file.Close()
			return nil, fmt.Errorf("could not read the trace file: %w", err)
		}
		w.needNewline = last[0] != '\n'
	}
	return w, nil
}

func (w *writer) run(results <-chan result) {
	for r := range results {
		if r.forwarded {
			w.summary.Forwarded++
			continue
		}
		event := r.event
		if r.line != nil {
			if err := w.append(r.line); err != nil {
				if w.err == nil {
					w.err = fmt.Errorf("could not write the trace file: %w", err)
				}
				event.Outcome = skipWriteFailed
			}
		}
		if event.Outcome == outcomeRecorded {
			w.summary.Recorded++
			part := event.Part
			if part == "" {
				part = UnnamedPart
			}
			w.summary.ByPart[part]++
		} else {
			w.summary.Skipped[event.Outcome]++
		}
		if w.events != nil {
			w.events(event)
		}
	}
}

func (w *writer) append(line []byte) error {
	if w.needNewline {
		line = append([]byte{'\n'}, line...)
	}
	_, err := w.file.Write(line)
	// After a failed write the file may end part way through a line.
	w.needNewline = err != nil
	return err
}

// close closes the file and returns the summary with the first write
// failure, if there was one.
func (w *writer) close() (Summary, error) {
	if err := w.file.Close(); err != nil && w.err == nil {
		w.err = fmt.Errorf("could not write the trace file: %w", err)
	}
	return w.summary, w.err
}
