// Tracked-field paths: a small JSONPath subset (`$.a.b[0]`, `a.b`,
// `$["a key"]`) with no wildcards, filters or recursion, because a tracked
// field names one value per output. Two readers: `parseJsonPath`/`extract`
// as the reference's statistics read a field, and `parsePath`/`valueAt` as
// the reference page does, which also takes `[]` for every item of a list.

import { isPlainObject } from "./py.ts";

export type PathToken = string | number;

// Python's `\w` is Unicode-aware, hence the property classes after the first character.
const PATH_TOKEN = /\.(?<name>[A-Za-z_$][\p{L}\p{N}_$-]*)|\[(?<index>-?[0-9]+)\]|\[(?<quote>["'])(?<key>(?:(?!\k<quote>)[^\n])*)\k<quote>\]/uy;

/** A tracked field path as keys and indexes. Throws on a path it cannot read. */
export function parseJsonPath(path: string): PathToken[] {
  let text = path.trim();
  if (text.startsWith("$")) text = text.slice(1);
  else if (text && !text.startsWith(".") && !text.startsWith("[")) text = `.${text}`;
  const tokens: PathToken[] = [];
  let position = 0;
  while (position < text.length) {
    PATH_TOKEN.lastIndex = position;
    const match = PATH_TOKEN.exec(text);
    const groups = match?.groups;
    if (!match || !groups) {
      throw new Error(`${JSON.stringify(path)} is not a supported JSON path (use $.field.child[0])`);
    }
    if (groups.name !== undefined) tokens.push(groups.name);
    else if (groups.index !== undefined) tokens.push(Number.parseInt(groups.index, 10));
    else tokens.push(groups.key as string);
    position = PATH_TOKEN.lastIndex;
  }
  if (!tokens.length) throw new Error(`${JSON.stringify(path)} names no field`);
  return tokens;
}

/** Marks a path that is not there, which is not the same as a null value. */
export const MISSING: unique symbol = Symbol("missing");

export function extract(value: unknown, path: readonly PathToken[]): unknown {
  let current = value;
  for (const token of path) {
    if (typeof token === "number") {
      if (!Array.isArray(current) || !(-current.length <= token && token < current.length)) return MISSING;
      current = current[token < 0 ? current.length + token : token];
    } else {
      if (!isPlainObject(current) || !Object.hasOwn(current, token)) return MISSING;
      current = current[token];
    }
  }
  return current;
}

// --- The page's reader ------------------------------------------------------

export type PageToken = { key: string } | { index: number } | { each: true };

const PAGE_TOKEN = /\.([A-Za-z_$][\w$-]*)|\[(-?\d+)\]|\[(["'])((?:(?!\3).)*)\3\]|\[\]/y;
const IDENTIFIER = /^[A-Za-z_$][\w$-]*$/;

function step(prefix: string, key: string): string {
  return IDENTIFIER.test(key) ? `${prefix}.${key}` : `${prefix}[${JSON.stringify(key)}]`;
}

/** Leaf paths of a value. An array of objects is walked as `$.a[].b`. */
export function jsonPaths(value: unknown, prefix = "$"): string[] {
  if (isPlainObject(value)) {
    return Object.keys(value).flatMap((key) => jsonPaths(value[key], step(prefix, key)));
  }
  if (Array.isArray(value) && value.some(isPlainObject)) {
    const keys = new Set<string>();
    for (const item of value) if (isPlainObject(item)) for (const k of Object.keys(item)) keys.add(k);
    return [...keys].flatMap((key) => {
      const holder = value.find((item) => isPlainObject(item) && key in item) as Record<string, unknown> | undefined;
      return jsonPaths(holder?.[key], step(`${prefix}[]`, key));
    });
  }
  return prefix === "$" ? [] : [prefix];
}

/** The page's tokens for a path, or null when it cannot read it. */
export function parsePath(path: string | null | undefined): PageToken[] | null {
  let text = String(path || "").trim();
  if (text.startsWith("$")) text = text.slice(1);
  else if (text && !/^[.[]/.test(text)) text = `.${text}`;
  const tokens: PageToken[] = [];
  PAGE_TOKEN.lastIndex = 0;
  while (PAGE_TOKEN.lastIndex < text.length) {
    const at = PAGE_TOKEN.lastIndex;
    const match = PAGE_TOKEN.exec(text);
    if (!match || match.index !== at) return null;
    if (match[1] !== undefined) tokens.push({ key: match[1] });
    else if (match[2] !== undefined) tokens.push({ index: Number(match[2]) });
    else if (match[4] !== undefined) tokens.push({ key: match[4] });
    else tokens.push({ each: true });
  }
  return tokens;
}

/** The value at a path, every item's for a `[]` path, or undefined. */
export function valueAt(value: unknown, path: string): unknown {
  const tokens = parsePath(path);
  if (!tokens) return undefined;
  let current: unknown[] = [value];
  let listed = false;
  for (const token of tokens) {
    current = current.flatMap((node) => {
      if ("each" in token) return Array.isArray(node) ? node : [];
      if ("index" in token) {
        if (!Array.isArray(node)) return [];
        const i = token.index < 0 ? node.length + token.index : token.index;
        return i >= 0 && i < node.length ? [node[i]] : [];
      }
      return isPlainObject(node) && token.key in node ? [node[token.key]] : [];
    });
    if ("each" in token) listed = true;
  }
  if (listed) return current.length ? current : undefined;
  return current[0];
}
