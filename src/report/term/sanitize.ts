// Captured text (prompts, outputs, errors, labels, file names) is untrusted:
// a model can return an escape sequence that clears the screen, moves the
// cursor, retitles the window or rewrites what the report shows. Every string
// the terminal report prints passes through `clean` first, which turns C0 and
// C1 controls into visible caret notation (ESC becomes "^["), normalizes line
// breaks and shows bidirectional overrides as code points, so the only
// escape sequences on screen are the ones the report writes itself.

const BIDI = /[؜‎‏‪-‮⁦-⁩]/g;

function caret(cp: number): string {
  if (cp === 0x7f) return "^?";
  if (cp < 0x20) return `^${String.fromCharCode(cp + 0x40)}`;
  return `<${cp.toString(16).toUpperCase().padStart(2, "0")}>`;
}

/**
 * Neutralize controls. Newlines survive (CR LF, CR, U+2028 and U+2029 become
 * "\n"); tabs become two spaces; every other C0 or C1 control, and DEL,
 * becomes caret notation.
 */
export function clean(text: string): string {
  return String(text)
    .replace(/\r\n?|[\u2028\u2029]/g, "\n")
    .replace(/\t/g, "  ")
    .replace(/[\x00-\x09\x0b-\x1f\x7f-\x9f]/g, (ch) => caret(ch.charCodeAt(0)))
    .replace(BIDI, (ch) => `<U+${(ch.codePointAt(0) as number).toString(16).toUpperCase()}>`);
}

/** `clean`, then one line: runs of whitespace (newlines included) become one space. */
export function cleanLine(text: string): string {
  return clean(text).replace(/\s+/g, " ").trim();
}

const ASCII_MAP: [RegExp, string][] = [
  [/[−–—]/g, "-"],
  [/×/g, "x"],
  [/…/g, "..."],
  [/[‘’]/g, "'"],
  [/[“”]/g, '"'],
  [/→/g, "->"],
  [/←/g, "<-"],
  [/↑/g, "^"],
  [/↓/g, "v"],
  [/≈/g, "~"],
  [/·/g, "-"],
  [/›/g, ">"],
  [/\u00a0/g, " "],
  [/[^\x1b\x20-\x7e\n]/g, "?"],
];

/** Printable ASCII only: the report's own glyphs mapped to look-alikes, anything else "?". */
export function toAscii(text: string): string {
  let out = text;
  for (const [re, to] of ASCII_MAP) out = out.replace(re, to);
  return out;
}

/**
 * A copy of `value` with every string cleaned (and made ASCII when asked).
 * Numbers, booleans and structure are untouched, so figures read the same.
 */
export function cleanDeep<T>(value: T, ascii = false): T {
  const seen = new Map<object, unknown>();
  const walk = (v: unknown): unknown => {
    if (typeof v === "string") {
      const c = clean(v);
      return ascii ? toAscii(c) : c;
    }
    if (!v || typeof v !== "object") return v;
    const known = seen.get(v as object);
    if (known) return known;
    if (Array.isArray(v)) {
      const out: unknown[] = [];
      seen.set(v, out);
      for (const item of v) out.push(walk(item));
      return out;
    }
    if (v instanceof Set || v instanceof Map) return v;
    const out: Record<string, unknown> = {};
    seen.set(v as object, out);
    for (const [k, item] of Object.entries(v as Record<string, unknown>)) out[k] = walk(item);
    return out;
  };
  return walk(value) as T;
}
