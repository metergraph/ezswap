// The fields a run could track, as the JSON tree the outputs share: objects
// as branches, the values a field can track as leaves. Matches Metergraph's
// hosted figures (conformance/page.json).

import { type Loose, fieldKind, parseOutput, pickerPaths } from "./fields.ts";
import { type PageToken, parsePath, valueAt } from "./paths.ts";
import type { FieldKind } from "./view-types.ts";

const IDENTIFIER = /^[A-Za-z_$][\w$-]*$/;
/** A tree with this many fields or fewer opens whole; a larger one opens only toward what is tracked. */
export const OPEN_ALL_MAX = 15;
const SAMPLE_MAX = 40;

function stepPath(prefix: string, token: PageToken): string {
  if ("each" in token) return `${prefix}[]`;
  if ("index" in token) return `${prefix}[${token.index}]`;
  return IDENTIFIER.test(token.key) ? `${prefix}.${token.key}` : `${prefix}[${JSON.stringify(token.key)}]`;
}

function stepLabel(token: PageToken): string {
  if ("each" in token) return "[ ]";
  if ("index" in token) return `[${token.index}]`;
  return token.key;
}

/** Production's own outputs that parse, in case order. */
function controlOutputs(cases: Loose[] | null | undefined): unknown[] {
  return (cases || [])
    .map((c) => parseOutput(c.control?.output))
    .filter((p) => p.ok)
    .map((p) => p.value);
}

/** How many outputs parsed as JSON at all, control and arms together. */
export function parsedOutputCount(cases: Loose[] | null | undefined): number {
  let n = 0;
  for (const c of cases || []) {
    for (const output of [c.control?.output, ...Object.values(c.arms || {}).map((a: Loose) => a?.output)]) {
      if (parseOutput(output).ok) n += 1;
    }
  }
  return n;
}

/** A field's first value in production's outputs, as a short preview. */
export function samplePreview(value: unknown): string {
  if (value === undefined) return "";
  if (Array.isArray(value)) return `[${value.length} ${value.length === 1 ? "item" : "items"}]`;
  if (value !== null && typeof value === "object") return "{ }";
  const text = typeof value === "string" ? JSON.stringify(value) : String(value);
  return text.length > SAMPLE_MAX ? `${text.slice(0, SAMPLE_MAX - 2)}…"` : text;
}

export interface FieldNode {
  path: string;
  key: string;
  children: FieldNode[];
  leaf: { path: string; count: number; total: number; kind: FieldKind; sample: string } | null;
}

/**
 * The tree: nodes under one root, children in the order their paths first
 * appear (most common first). A leaf carries how many outputs had it, out of
 * how many parsed, its kind over production's values and a preview of one.
 */
export function fieldTree(cases: Loose[] | null | undefined, tracked: string[] = []): FieldNode {
  const paths = pickerPaths(cases, tracked);
  const controls = controlOutputs(cases);
  const total = parsedOutputCount(cases);
  const root: FieldNode = { path: "$", key: "output", children: [], leaf: null };
  const index = new Map<string, FieldNode>([["$", root]]);
  for (const { path, count } of paths) {
    const tokens = parsePath(path);
    if (!tokens || !tokens.length) continue;
    let node = root;
    let prefix = "$";
    for (const token of tokens) {
      prefix = stepPath(prefix, token);
      let next = index.get(prefix);
      if (!next) {
        next = { path: prefix, key: stepLabel(token), children: [], leaf: null };
        index.set(prefix, next);
        node.children.push(next);
      }
      node = next;
    }
    const values = controls.map((value) => valueAt(value, path));
    node.leaf = { path, count, total, kind: fieldKind(values), sample: samplePreview(values.find((v) => v !== undefined)) };
  }
  return root;
}

/** Every leaf path at or under a node, in tree order. */
export function leafPaths(node: FieldNode): string[] {
  return [...(node.leaf ? [node.leaf.path] : []), ...node.children.flatMap(leafPaths)];
}

/** A branch's state against the tracked set. */
export function branchState(node: FieldNode, tracked: string[]): { state: "all" | "some" | "none"; on: number; total: number } {
  const leaves = leafPaths(node);
  const on = leaves.filter((p) => tracked.includes(p)).length;
  return { state: on === 0 ? "none" : on === leaves.length ? "all" : "some", on, total: leaves.length };
}

/** Track everything under a branch, or untrack it when all of it is tracked. Order is kept. */
export function toggleBranch(node: FieldNode, tracked: string[]): string[] {
  const leaves = leafPaths(node);
  if (leaves.every((p) => tracked.includes(p))) return tracked.filter((p) => !leaves.includes(p));
  return [...tracked, ...leaves.filter((p) => !tracked.includes(p))];
}

/** Which branches start open: all for a small tree, else those leading to a focused field (the root always). */
export function initialOpen(root: FieldNode, focus: string[] = []): Set<string> {
  const open = new Set(["$"]);
  const all = leafPaths(root);
  const walk = (node: FieldNode) => {
    if (!node.children.length) return;
    const leaves = leafPaths(node);
    if (all.length <= OPEN_ALL_MAX || leaves.some((p) => focus.includes(p))) open.add(node.path);
    node.children.forEach(walk);
  };
  walk(root);
  return open;
}
