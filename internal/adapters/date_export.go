package adapters

// Exported for the runner, which orders cases and spans traffic by time the
// way the TypeScript does with Date.parse and toISOString.

// DateParse is Date.parse in milliseconds, false for an invalid date.
func DateParse(s string) (float64, bool) { return parseDate(s) }

// ISOString is Date.prototype.toISOString for a valid time value.
func ISOString(ms float64) string { return isoString(ms) }
