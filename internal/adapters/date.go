package adapters

import (
	"fmt"
	"math"
	"time"

	"github.com/metergraph/ezswap/internal/cases/jsval"
)

// JavaScript's Date, as far as the adapters use it: `new Date(string)` and
// `new Date(ms).toISOString()`.
//
// Date.parse beyond the ISO format is implementation-defined. This follows
// V8's parser (src/date/dateparser-inl.h), the one Node runs: the ES5 ISO
// form first (a date alone is UTC, a date and time without an offset local
// time), then V8's legacy rules for everything else ("2026-03-04 12:30",
// "Mar 4 2026", "Wed, 04 Mar 2026 12:30:00 GMT", ...).

// maxTimeMs is the largest magnitude a Date holds (TimeClip).
const maxTimeMs = 8.64e15

// timeClip is TimeClip: NaN past the Date range, otherwise truncated to an
// integer.
func timeClip(ms float64) (float64, bool) {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || math.Abs(ms) > maxTimeMs {
		return 0, false
	}
	return math.Trunc(ms) + 0, true
}

// ---------------------------------------------------------------------------
// Tokens

type tokenKind int

const (
	tokInvalid tokenKind = iota
	tokEnd
	tokNumber
	tokSymbol
	tokWhiteSpace
	tokKeyword
	tokUnknown
)

type keywordType int

const (
	kwInvalid keywordType = iota
	kwMonthName
	kwTimeZoneName
	kwTimeSeparator
	kwAmPm
)

type dateToken struct {
	kind    tokenKind
	value   int // number, symbol or keyword value
	length  int
	keyword keywordType
	prefix  string // a keyword's first three letters, lower case
}

func (t dateToken) isNumber() bool                 { return t.kind == tokNumber }
func (t dateToken) isFixedLengthNumber(n int) bool { return t.kind == tokNumber && t.length == n }
func (t dateToken) isSymbol(c rune) bool           { return t.kind == tokSymbol && t.value == int(c) }
func (t dateToken) isAsciiSign() bool              { return t.isSymbol('+') || t.isSymbol('-') }
func (t dateToken) asciiSign() int {
	if t.isSymbol('-') {
		return -1
	}
	return 1
}
func (t dateToken) isKeywordType(k keywordType) bool { return t.kind == tokKeyword && t.keyword == k }
func (t dateToken) isKeywordZ() bool {
	return t.kind == tokKeyword && t.keyword == kwTimeZoneName && t.length == 1 && t.value == 0 && t.prefix == "z"
}

var keywords = []struct {
	prefix string
	kind   keywordType
	value  int
}{
	{"jan", kwMonthName, 1}, {"feb", kwMonthName, 2}, {"mar", kwMonthName, 3}, {"apr", kwMonthName, 4},
	{"may", kwMonthName, 5}, {"jun", kwMonthName, 6}, {"jul", kwMonthName, 7}, {"aug", kwMonthName, 8},
	{"sep", kwMonthName, 9}, {"oct", kwMonthName, 10}, {"nov", kwMonthName, 11}, {"dec", kwMonthName, 12},
	{"am", kwAmPm, 0}, {"pm", kwAmPm, 12},
	{"ut", kwTimeZoneName, 0}, {"utc", kwTimeZoneName, 0}, {"z", kwTimeZoneName, 0}, {"gmt", kwTimeZoneName, 0},
	{"cdt", kwTimeZoneName, -5}, {"cst", kwTimeZoneName, -6}, {"edt", kwTimeZoneName, -4}, {"est", kwTimeZoneName, -5},
	{"mdt", kwTimeZoneName, -6}, {"mst", kwTimeZoneName, -7}, {"pdt", kwTimeZoneName, -7}, {"pst", kwTimeZoneName, -8},
	{"t", kwTimeSeparator, 0},
}

type dateScanner struct {
	in   []rune
	pos  int
	next dateToken
}

func newDateScanner(s string) *dateScanner {
	sc := &dateScanner{in: []rune(s)}
	sc.next = sc.scan()
	return sc
}

func (sc *dateScanner) ch() rune {
	if sc.pos < len(sc.in) {
		return sc.in[sc.pos]
	}
	return 0
}

func (sc *dateScanner) scan() dateToken {
	start := sc.pos
	if sc.pos >= len(sc.in) {
		return dateToken{kind: tokEnd}
	}
	c := sc.ch()
	if c >= '0' && c <= '9' {
		// Leading zeros are skipped; nine significant digits are kept.
		for sc.ch() == '0' {
			sc.pos++
		}
		n, digits := 0, 0
		for c := sc.ch(); c >= '0' && c <= '9'; c = sc.ch() {
			if digits < 9 {
				n = n*10 + int(c-'0')
			}
			digits++
			sc.pos++
		}
		return dateToken{kind: tokNumber, value: n, length: sc.pos - start}
	}
	switch c {
	case ':', '-', '+', '.', ')':
		sc.pos++
		return dateToken{kind: tokSymbol, value: int(c), length: 1}
	}
	if c >= 'A' && !jsval.IsSpace(c) {
		prefix := []rune{}
		for c := sc.ch(); c >= 'A' && !jsval.IsSpace(c); c = sc.ch() {
			if len(prefix) < 3 {
				prefix = append(prefix, c|0x20)
			}
			sc.pos++
		}
		length := sc.pos - start
		word := string(prefix)
		for _, k := range keywords {
			if k.prefix == word && (length <= 3 || k.kind == kwMonthName) {
				return dateToken{kind: tokKeyword, keyword: k.kind, value: k.value, length: length, prefix: word}
			}
		}
		return dateToken{kind: tokKeyword, keyword: kwInvalid, length: length, prefix: word}
	}
	if jsval.IsSpace(c) {
		for sc.pos < len(sc.in) && jsval.IsSpace(sc.ch()) {
			sc.pos++
		}
		return dateToken{kind: tokWhiteSpace, length: sc.pos - start}
	}
	if c == '(' {
		balance := 0
		for done := false; !done; {
			switch sc.ch() {
			case ')':
				balance--
			case '(':
				balance++
			}
			sc.pos++
			// do { ... } while (balance > 0 && ch)
			done = balance <= 0 || sc.ch() == 0
		}
		return dateToken{kind: tokUnknown}
	}
	sc.pos++
	return dateToken{kind: tokUnknown}
}

func (sc *dateScanner) Next() dateToken {
	t := sc.next
	sc.next = sc.scan()
	return t
}

func (sc *dateScanner) Peek() dateToken { return sc.next }

func (sc *dateScanner) SkipSymbol(c rune) bool {
	if sc.next.isSymbol(c) {
		sc.Next()
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Composers

const none = math.MinInt32

func between(x, lo, hi int) bool { return x >= lo && x <= hi }
func isMonth(x int) bool         { return between(x, 1, 12) }
func isDay(x int) bool           { return between(x, 1, 31) }
func isHour(x int) bool          { return between(x, 0, 23) }
func isMinute(x int) bool        { return between(x, 0, 59) }
func isSecond(x int) bool        { return between(x, 0, 59) }
func isHour12(x int) bool        { return between(x, 0, 12) }
func isMillisecond(x int) bool   { return between(x, 0, 999) }

type dayComposer struct {
	comp       [3]int
	index      int
	namedMonth int
	isoDate    bool
}

func (d *dayComposer) add(n int) bool {
	if d.index < 3 {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func (d *dayComposer) write() (year, month, day int, ok bool) {
	if d.index < 1 {
		return 0, 0, 0, false
	}
	// Day and month default to 1.
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	year = 0 // Default year is 0 (=> 2000) for KJS compatibility.
	if d.namedMonth == none {
		if d.isoDate || !isDay(d.comp[0]) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2] // YMD
		} else {
			month, day, year = d.comp[0], d.comp[1], d.comp[2] // MD(Y)
		}
	} else {
		month = d.namedMonth
		if !isDay(d.comp[0]) {
			year, day = d.comp[0], d.comp[1] // YMD, MYD, or YDM
		} else {
			day, year = d.comp[0], d.comp[1] // DMY, MDY, or DYM
		}
	}
	if !d.isoDate {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	if !isMonth(month) || !isDay(day) {
		return 0, 0, 0, false
	}
	return year, month, day, true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *timeComposer) isEmpty() bool { return t.index == 0 }
func (t *timeComposer) isExpecting(n int) bool {
	return (t.index == 1 && isMinute(n)) || (t.index == 2 && isSecond(n)) || (t.index == 3 && isMillisecond(n))
}
func (t *timeComposer) add(n int) bool {
	if t.index < 4 {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}
func (t *timeComposer) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *timeComposer) write() (h, m, s, ms int, ok bool) {
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	h, m, s, ms = t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != none {
		if !isHour12(h) {
			return 0, 0, 0, 0, false
		}
		h %= 12
		h += t.hourOffset
	}
	if !isHour(h) || !isMinute(m) || !isSecond(s) || !isMillisecond(ms) {
		// A 24th hour is allowed if minutes, seconds, and milliseconds are 0.
		if h != 24 || m != 0 || s != 0 || ms != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return h, m, s, ms, true
}

type tzComposer struct{ sign, hour, minute int }

func (z *tzComposer) set(offsetHours int) {
	z.sign = 1
	if offsetHours < 0 {
		z.sign = -1
		offsetHours = -offsetHours
	}
	z.hour, z.minute = offsetHours, 0
}
func (z *tzComposer) isExpecting(n int) bool {
	return z.hour != none && z.minute == none && isMinute(n)
}
func (z *tzComposer) isUTC() bool   { return z.hour == 0 && z.minute == 0 }
func (z *tzComposer) isEmpty() bool { return z.hour == none }

// write is the UTC offset in seconds, or local time when local is true.
func (z *tzComposer) write() (offset int, local bool, ok bool) {
	if z.sign == none {
		return 0, true, true
	}
	if z.hour == none {
		z.hour = 0
	}
	if z.minute == none {
		z.minute = 0
	}
	total := z.hour*3600 + z.minute*60
	if total > 1<<30-1 {
		return 0, false, false
	}
	return z.sign * total, false, true
}

func readMilliseconds(t dateToken) int {
	number, length := t.value, t.length
	if length < 3 {
		// Put the most significant digit in the hundreds position.
		if length == 1 {
			number *= 100
		} else if length == 2 {
			number *= 10
		}
	} else if length > 3 {
		if length > 9 {
			length = 9
		}
		for length > 3 {
			number /= 10
			length--
		}
	}
	return number
}

// parseES5 reads the ES5 ISO form. It returns the first token it did not
// handle (end of input when the whole string was ISO), or an invalid token.
func parseES5(sc *dateScanner, day *dayComposer, tm *timeComposer, tz *tzComposer) dateToken {
	// Mandatory date: [('-'|'+')yy]yyyy['-'MM['-'DD]]
	if sc.Peek().isAsciiSign() {
		// Keep the sign token, so invalid dates can be detected.
		signToken := sc.Next()
		if !sc.Peek().isFixedLengthNumber(6) {
			return signToken
		}
		sign := signToken.asciiSign()
		year := sc.Next().value
		if sign < 0 && year == 0 {
			return signToken
		}
		day.add(sign * year)
	} else if sc.Peek().isFixedLengthNumber(4) {
		day.add(sc.Next().value)
	} else {
		return sc.Next()
	}
	if sc.SkipSymbol('-') {
		if !sc.Peek().isFixedLengthNumber(2) || !isMonth(sc.Peek().value) {
			return sc.Next()
		}
		day.add(sc.Next().value)
		if sc.SkipSymbol('-') {
			if !sc.Peek().isFixedLengthNumber(2) || !isDay(sc.Peek().value) {
				return sc.Next()
			}
			day.add(sc.Next().value)
		}
	}
	// Optional time: 'T'HH':'mm[':'ss['.'sss]]Z
	if !sc.Peek().isKeywordType(kwTimeSeparator) {
		if sc.Peek().kind != tokEnd {
			return sc.Next()
		}
	} else {
		sc.Next()
		if !sc.Peek().isFixedLengthNumber(2) || !between(sc.Peek().value, 0, 24) {
			return dateToken{kind: tokInvalid}
		}
		// Allow 24:00[:00[.000]], but no other time starting with 24.
		hourIs24 := sc.Peek().value == 24
		tm.add(sc.Next().value)
		if !sc.SkipSymbol(':') {
			return dateToken{kind: tokInvalid}
		}
		if !sc.Peek().isFixedLengthNumber(2) || !isMinute(sc.Peek().value) || (hourIs24 && sc.Peek().value > 0) {
			return dateToken{kind: tokInvalid}
		}
		tm.add(sc.Next().value)
		if sc.SkipSymbol(':') {
			if !sc.Peek().isFixedLengthNumber(2) || !isSecond(sc.Peek().value) || (hourIs24 && sc.Peek().value > 0) {
				return dateToken{kind: tokInvalid}
			}
			tm.add(sc.Next().value)
			if sc.SkipSymbol('.') {
				if !sc.Peek().isNumber() || (hourIs24 && sc.Peek().value > 0) {
					return dateToken{kind: tokInvalid}
				}
				// Allow more or less than the mandated three digits.
				tm.add(readMilliseconds(sc.Next()))
			}
		}
		// Optional time zone: 'Z' | ('+'|'-')hh':'mm
		if sc.Peek().isKeywordZ() {
			sc.Next()
			tz.set(0)
		} else if sc.Peek().isSymbol('+') || sc.Peek().isSymbol('-') {
			tz.sign = sc.Next().asciiSign()
			if sc.Peek().isFixedLengthNumber(4) {
				// hhmm extension syntax.
				hourmin := sc.Next().value
				hour, minute := hourmin/100, hourmin%100
				if !isHour(hour) || !isMinute(minute) {
					return dateToken{kind: tokInvalid}
				}
				tz.hour, tz.minute = hour, minute
			} else {
				if !sc.Peek().isFixedLengthNumber(2) || !isHour(sc.Peek().value) {
					return dateToken{kind: tokInvalid}
				}
				tz.hour = sc.Next().value
				if !sc.SkipSymbol(':') {
					return dateToken{kind: tokInvalid}
				}
				if !sc.Peek().isFixedLengthNumber(2) || !isMinute(sc.Peek().value) {
					return dateToken{kind: tokInvalid}
				}
				tz.minute = sc.Next().value
			}
		}
		if sc.Peek().kind != tokEnd {
			return dateToken{kind: tokInvalid}
		}
	}
	// A date alone is UTC; a date and time without an offset is local time.
	if tz.isEmpty() && tm.isEmpty() {
		tz.set(0)
	}
	day.isoDate = true
	return dateToken{kind: tokEnd}
}

// parseDate is Date.parse in milliseconds, false for an invalid date.
func parseDate(s string) (float64, bool) {
	sc := newDateScanner(s)
	day := &dayComposer{namedMonth: none}
	tm := &timeComposer{hourOffset: none}
	tz := &tzComposer{sign: none, hour: none, minute: none}

	token := parseES5(sc, day, tm, tz)
	if token.kind == tokInvalid {
		return 0, false
	}
	hasReadNumber := day.index > 0
	// Anything left goes through the legacy parser.
	for ; token.kind != tokEnd; token = sc.Next() {
		switch {
		case token.isNumber():
			hasReadNumber = true
			n := token.value
			if sc.SkipSymbol(':') {
				if sc.SkipSymbol(':') {
					// n + "::"
					if !tm.isEmpty() {
						return 0, false
					}
					tm.add(n)
					tm.add(0)
				} else {
					// n + ":"
					if !tm.add(n) {
						return 0, false
					}
					if sc.Peek().isSymbol('.') {
						sc.Next()
					}
				}
			} else if sc.SkipSymbol('.') && tm.isExpecting(n) {
				tm.add(n)
				if !sc.Peek().isNumber() {
					return 0, false
				}
				ms := readMilliseconds(sc.Next())
				if ms < 0 {
					return 0, false
				}
				tm.addFinal(ms)
			} else if tz.isExpecting(n) {
				tz.minute = n
			} else if tm.isExpecting(n) {
				tm.addFinal(n)
				// Require end, white space, "Z", "+" or "-" right after the time.
				peek := sc.Peek()
				if peek.kind != tokEnd && peek.kind != tokWhiteSpace && !peek.isKeywordZ() && !peek.isAsciiSign() {
					return 0, false
				}
			} else {
				if !day.add(n) {
					return 0, false
				}
				sc.SkipSymbol('-')
			}
		case token.kind == tokKeyword:
			if token.keyword == kwAmPm && !tm.isEmpty() {
				tm.hourOffset = token.value
			} else if token.keyword == kwMonthName {
				day.namedMonth = token.value
				sc.SkipSymbol('-')
			} else if token.keyword == kwTimeZoneName && hasReadNumber {
				tz.set(token.value)
			} else {
				// Garbage words are illegal once a number has been read, and the
				// first number must be separated from a garbage word.
				if hasReadNumber || sc.Peek().isNumber() {
					return 0, false
				}
			}
		case token.isAsciiSign() && (tz.isUTC() || !tm.isEmpty()):
			// A UTC offset (only after UTC or a time).
			tz.sign = token.asciiSign()
			n, length := 0, 0
			if sc.Peek().isNumber() {
				next := sc.Next()
				n, length = next.value, next.length
			}
			hasReadNumber = true
			switch {
			case sc.Peek().isSymbol(':'):
				tz.hour, tz.minute = n, none
			case length == 1 || length == 2:
				// GMT-8
				tz.hour, tz.minute = n, 0
			case length == 3 || length == 4:
				// GMT-0800
				tz.hour, tz.minute = n/100, n%100
			default:
				return 0, false
			}
		case (token.isAsciiSign() || token.isSymbol(')')) && hasReadNumber:
			// An extra sign or ')' is illegal once a number has been read.
			return 0, false
		default:
			// Other characters and white space are ignored.
		}
	}

	year, month, dayOfMonth, ok := day.write()
	if !ok {
		return 0, false
	}
	hour, minute, second, milli, ok := tm.write()
	if !ok {
		return 0, false
	}
	offset, local, ok := tz.write()
	if !ok {
		return 0, false
	}
	// Past ±275760 years the time value is out of range anyway; stop before
	// time.Time arithmetic overflows.
	if year > 300000 || year < -300000 {
		return 0, false
	}
	t := time.Date(year, time.Month(month), dayOfMonth, hour, minute, second, milli*int(time.Millisecond), time.UTC)
	ms := float64(t.UnixMilli())
	if local {
		ms = localToUTC(t.UnixMilli())
	} else {
		ms -= float64(offset) * 1000
	}
	return timeClip(ms)
}

// localOffsetMs is the local zone's UTC offset at an instant.
func localOffsetMs(instant int64) int64 {
	_, offset := time.UnixMilli(instant).In(time.Local).Zone()
	return int64(offset) * 1000
}

// localToUTC is UTC(t) for a local wall time held as if it were UTC. A wall
// time that occurs twice (clocks going back) is the earlier instant; one that
// does not occur (clocks going forward) is read with the offset before the
// change, as JavaScript does. time.Date leaves both choices unspecified.
func localToUTC(wall int64) float64 {
	const window = 36 * 3600 * 1000
	before, after := localOffsetMs(wall-window), localOffsetMs(wall+window)
	best, found := int64(0), false
	for _, offset := range []int64{before, after} {
		instant := wall - offset
		if localOffsetMs(instant) == offset && (!found || instant < best) {
			best, found = instant, true
		}
	}
	if !found {
		best = wall - before
	}
	return float64(best)
}

// isoString is Date.prototype.toISOString for a valid time value.
func isoString(ms float64) string {
	t := time.UnixMilli(int64(ms)).UTC()
	year := t.Year()
	var y string
	switch {
	case year >= 0 && year <= 9999:
		y = fmt.Sprintf("%04d", year)
	case year < 0:
		y = fmt.Sprintf("-%06d", -year)
	default:
		y = fmt.Sprintf("+%06d", year)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ",
		y, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/int(time.Millisecond))
}
