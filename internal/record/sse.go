package record

import "bytes"

// sseParser reads a server-sent event stream fed to it in arbitrary pieces
// and calls emit once per event that carries data. It follows the parsing
// rules of the HTML standard: lines end with \n, \r\n or \r; a line that
// starts with a colon is a comment; several data lines join with \n; a
// blank line ends the event. An event still open when the stream ends is
// never emitted.
type sseParser struct {
	emit func(event string, data []byte)

	line    []byte // the line being read
	afterCR bool   // the last byte was \r: a \n that follows belongs to it
	started bool   // past the first line, where a byte order mark may sit
	event   string
	data    []byte
	hasData bool
}

var byteOrderMark = []byte{0xEF, 0xBB, 0xBF}

func (p *sseParser) feed(chunk []byte) {
	for _, b := range chunk {
		if p.afterCR {
			p.afterCR = false
			if b == '\n' {
				continue
			}
		}
		switch b {
		case '\n':
			p.endLine()
		case '\r':
			p.endLine()
			p.afterCR = true
		default:
			p.line = append(p.line, b)
		}
	}
}

func (p *sseParser) endLine() {
	line := p.line
	p.line = p.line[:0]
	if !p.started {
		p.started = true
		line = bytes.TrimPrefix(line, byteOrderMark)
	}
	if len(line) == 0 {
		if p.hasData {
			p.emit(p.event, p.data)
		}
		p.event, p.data, p.hasData = "", p.data[:0], false
		return
	}
	if line[0] == ':' {
		return
	}
	name, value, _ := bytes.Cut(line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" ")) // one leading space is part of the syntax
	switch string(name) {
	case "event":
		p.event = string(value)
	case "data":
		if p.hasData {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, value...)
		p.hasData = true
	}
}
