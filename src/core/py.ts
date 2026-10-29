// Python semantics the per-arm summary and the noise floor depend on. Their
// figures must match Metergraph's hosted figures to the last bit
// (conformance/summarize.json, conformance/noise_floor.json), so where Python
// and JavaScript disagree on rounding, summation, whitespace or string length,
// these follow Python.

/** Python's `str.isspace()` characters, which `str.strip()` and `\s` use. */
const PY_WS = "\\t\\n\\x0b\\x0c\\r\\x1c-\\x1f \\x85\\xa0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000";
export const PY_WS_CLASS = `[${PY_WS}]`;
const STRIP = new RegExp(`^[${PY_WS}]+|[${PY_WS}]+$`, "g");

/** `str.strip()`: unlike `String.prototype.trim`, keeps U+FEFF and strips U+001C..U+001F and U+0085. */
export function pyStrip(text: string): string {
  return text.replace(STRIP, "");
}

/** `round(x, digits)`: correctly rounded, exact ties to even. */
export function pyRound(x: number, digits: number): number {
  if (!Number.isFinite(x) || Math.abs(x) >= 1e21) return x;
  const exact = Math.abs(x).toFixed(100);
  const point = exact.indexOf(".");
  const rest = exact.slice(point + 1 + digits);
  const tie = rest[0] === "5" && /^50*$/.test(rest);
  if (tie) {
    const kept = exact.slice(0, digits > 0 ? point + 1 + digits : point);
    const last = Number(kept[kept.length - 1]);
    if (last % 2 === 0) return Number((x < 0 ? "-" : "") + kept);
  }
  return Number(x.toFixed(digits));
}

/**
 * `sum()` over floats. Python 3.12 and later add floats with Neumaier's
 * compensated summation, so `sum([0.1] * 10)` is exactly 1.0.
 */
export function pySum(values: readonly number[]): number {
  if (values.length === 0) return 0;
  let total = 0 + (values[0] as number);
  let compensation = 0;
  for (let i = 1; i < values.length; i += 1) {
    const x = values[i] as number;
    const t = total + x;
    if (Math.abs(total) >= Math.abs(x)) compensation += total - t + x;
    else compensation += x - t + total;
    total = t;
  }
  if (compensation && Number.isFinite(compensation)) total += compensation;
  return total;
}

/** `statistics.median`: the middle value, or the mean of the two middle ones. */
export function pyMedian(values: readonly number[]): number | null {
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const n = sorted.length;
  const mid = Math.floor(n / 2);
  if (n % 2 === 1) return sorted[mid] as number;
  return ((sorted[mid - 1] as number) + (sorted[mid] as number)) / 2;
}

/** Linear-interpolated (inclusive) quantile. */
export function pyQuantile(values: readonly number[], q: number): number | null {
  if (values.length === 0) return null;
  const ordered = [...values].sort((a, b) => a - b);
  const position = (ordered.length - 1) * q;
  const low = Math.trunc(position);
  const high = Math.min(low + 1, ordered.length - 1);
  const lo = ordered[low] as number;
  const hi = ordered[high] as number;
  return lo + (hi - lo) * (position - low);
}

/** `len(text)`: code points, not UTF-16 units. */
export function codePointLength(text: string): number {
  let n = 0;
  for (const _ of text) n += 1;
  return n;
}

/** `text[:n]` in code points. */
export function codePointSlice(text: string, n: number): string {
  let out = "";
  let i = 0;
  for (const ch of text) {
    if (i >= n) break;
    out += ch;
    i += 1;
  }
  return out;
}

/** Python's string order: by code point, where JavaScript compares UTF-16 units. */
export function codePointCompare(a: string, b: string): number {
  const left = [...a];
  const right = [...b];
  const n = Math.min(left.length, right.length);
  for (let i = 0; i < n; i += 1) {
    const x = (left[i] as string).codePointAt(0) as number;
    const y = (right[i] as string).codePointAt(0) as number;
    if (x !== y) return x - y;
  }
  return left.length - right.length;
}

export function pySorted(values: Iterable<string>): string[] {
  return [...values].sort(codePointCompare);
}

/** Python truthiness: empty strings, lists and objects are false. */
export function pyTruthy(value: unknown): boolean {
  if (value == null || value === false || value === 0 || value === "") return false;
  if (Number.isNaN(value)) return false;
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === "object") return Object.keys(value as object).length > 0;
  return true;
}

export function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

/**
 * A number as Python would print it. A JSON number arrives here without
 * Python's int/float distinction, so an integral value below 1e16 is taken
 * for an int (as JSON integers are) and everything else for a float.
 */
export function pyNumberText(x: number): string {
  if (Number.isNaN(x)) return "NaN";
  if (x === Infinity) return "Infinity";
  if (x === -Infinity) return "-Infinity";
  if (Number.isInteger(x) && Math.abs(x) < 1e16) return Object.is(x, -0) ? "-0.0" : String(x);
  return pyFloatRepr(x);
}

/** `repr(float)`: shortest round-trip digits, exponent form below 1e-4 or from 1e16. */
export function pyFloatRepr(x: number): string {
  if (Number.isNaN(x)) return "nan";
  if (!Number.isFinite(x)) return x > 0 ? "inf" : "-inf";
  if (x === 0) return Object.is(x, -0) ? "-0.0" : "0.0";
  const sign = x < 0 ? "-" : "";
  const [mantissa, exponentText] = Math.abs(x).toExponential().split("e") as [string, string];
  const exponent = Number(exponentText);
  const digits = mantissa.replace(".", "");
  if (exponent < -4 || exponent >= 16) {
    const body = digits.length > 1 ? `${digits[0]}.${digits.slice(1)}` : digits;
    const e = Math.abs(exponent) < 10 ? `0${Math.abs(exponent)}` : String(Math.abs(exponent));
    return `${sign}${body}e${exponent < 0 ? "-" : "+"}${e}`;
  }
  if (exponent < 0) return `${sign}0.${"0".repeat(-exponent - 1)}${digits}`;
  const whole = digits.slice(0, exponent + 1).padEnd(exponent + 1, "0");
  const fraction = digits.slice(exponent + 1);
  return `${sign}${whole}.${fraction || "0"}`;
}

/** `str(value)` for a JSON value: None, True and False are spelt as Python spells them. */
export function pyStr(value: unknown): string {
  if (value === null || value === undefined) return "None";
  if (value === true) return "True";
  if (value === false) return "False";
  if (typeof value === "number") return Number.isInteger(value) && Math.abs(value) < 1e16 ? String(value) : pyFloatRepr(value);
  if (typeof value === "string") return value;
  return pyJsonDumps(value);
}

function pyJsonString(text: string): string {
  let out = '"';
  for (let i = 0; i < text.length; i += 1) {
    const code = text.charCodeAt(i);
    const ch = text[i] as string;
    if (ch === '"') out += '\\"';
    else if (ch === "\\") out += "\\\\";
    else if (ch === "\n") out += "\\n";
    else if (ch === "\r") out += "\\r";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\b") out += "\\b";
    else if (ch === "\f") out += "\\f";
    else if (code < 0x20 || code > 0x7e) {
      // ensure_ascii: every non-ASCII UTF-16 unit as \uXXXX (DEL stays as is).
      out += code === 0x7f ? ch : `\\u${code.toString(16).padStart(4, "0")}`;
    } else out += ch;
  }
  return `${out}"`;
}

/**
 * `json.dumps(value)` with ensure_ascii, used to bound and to compare
 * values. `compact` is `separators=(",", ":")`.
 */
export function pyJsonDumps(value: unknown, options: { sortKeys?: boolean; compact?: boolean } = {}): string {
  const item = options.compact ? "," : ", ";
  const pair = options.compact ? ":" : ": ";
  const dump = (v: unknown): string => {
    if (v === null || v === undefined) return "null";
    if (v === true) return "true";
    if (v === false) return "false";
    if (typeof v === "number") return pyNumberText(v);
    if (typeof v === "string") return pyJsonString(v);
    if (Array.isArray(v)) return `[${v.map(dump).join(item)}]`;
    if (typeof v === "object") {
      const keys = Object.keys(v as object);
      const ordered = options.sortKeys ? pySorted(keys) : keys;
      return `{${ordered.map((k) => `${pyJsonString(k)}${pair}${dump((v as Record<string, unknown>)[k])}`).join(item)}}`;
    }
    return pyJsonString(String(v));
  };
  return dump(value);
}

/** `len(json.dumps(value).encode("utf-8"))`: the output is ASCII, so its length. */
export function pyJsonBytes(value: unknown): number {
  return pyJsonDumps(value).length;
}

/** Python `==` on the values the statistics compare: scalars, and tuples of them. */
export function pyEqual(a: unknown, b: unknown): boolean {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((v, i) => pyEqual(v, b[i]));
  return a === b;
}
