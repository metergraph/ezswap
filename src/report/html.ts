// The single-file HTML report. Every captured string (prompts, outputs,
// model names, labels, file names) travels in one JSON block that the page's
// script renders with textContent, so none of it is ever parsed as HTML. The
// markup below holds only this module's own text. No network: inline CSS and
// script, a Content-Security-Policy that allows nothing else, no fonts.

import type { RunFile } from "../types.ts";
import { type ReportModel, NOISE_FLOOR_DOC, SETUP_COMMAND, reportModel } from "./model.ts";
import { hostedLink } from "./link.ts";

export interface HtmlOptions {
  tracked?: string[];
  labels?: boolean;
  monthlyCalls?: number | null;
}

/**
 * JSON that is safe inside a <script> element: no "<" (so no "</script>" or
 * "<!--"), no ">" or "&", and no line or paragraph separators.
 */
export function scriptJson(value: unknown): string {
  return JSON.stringify(value)
    .replace(/</g, "\\u003c")
    .replace(/>/g, "\\u003e")
    .replace(/&/g, "\\u0026")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
}

/** Escape text for an HTML text node or a double-quoted attribute. */
export function escapeHtml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

export function pageData(run: RunFile, model: ReportModel): Record<string, unknown> {
  const { view: _view, ...rest } = model;
  void _view;
  return {
    ...rest,
    links: { noiseFloor: NOISE_FLOOR_DOC, hosted: hostedLink(run, "report"), setup: SETUP_COMMAND },
    tool: { name: run.tool.name, version: run.tool.version },
  };
}

export function renderPage(run: RunFile, options: HtmlOptions = {}): string {
  const model = reportModel(run, options);
  const data = scriptJson(pageData(run, model));
  // The title is the one captured string outside the data block, so it is escaped.
  const title = escapeHtml(`Replay report: ${model.productionModel}`);
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'">
<meta name="referrer" content="no-referrer">
<meta name="color-scheme" content="light dark">
<title>${title}</title>
<style>${CSS}</style>
</head>
<body>
<main id="app">
<noscript><p class="noscript">This report draws its tables with a small inline script. Open it in a browser with JavaScript on; it makes no network requests.</p></noscript>
</main>
<script type="application/json" id="report-data">${data}</script>
<script>${SCRIPT}</script>
</body>
</html>
`;
}

const CSS = `
:root {
  --bg: #f7f6f4;
  --surface: #ffffff;
  --well: #f1efec;
  --text: #1b1b1a;
  --muted: #5d5b57;
  --faint: #8b8882;
  --line: #e3e0db;
  --line-strong: #cfcbc4;
  --accent: #1f7a5c;
  --accent-ink: #ffffff;
  --accent-soft: #e2f1eb;
  --beyond: #8a5a00;
  --beyond-soft: #fbefd6;
  --within: #3d5a73;
  --within-soft: #e5edf4;
  --danger: #a33a2b;
  --danger-soft: #f8e4e0;
  --floor: #6b5ea8;
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  --sans: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  color-scheme: light;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #141413;
    --surface: #1c1c1b;
    --well: #232321;
    --text: #ecebe8;
    --muted: #b0ada7;
    --faint: #85827c;
    --line: #2f2e2c;
    --line-strong: #44423f;
    --accent: #4fb892;
    --accent-ink: #0d1f18;
    --accent-soft: #183229;
    --beyond: #f0c46a;
    --beyond-soft: #3a2e14;
    --within: #9cc0e0;
    --within-soft: #1d2b38;
    --danger: #f08f80;
    --danger-soft: #3b1f1a;
    --floor: #b3a8ec;
    color-scheme: dark;
  }
}
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }
body { margin: 0; background: var(--bg); color: var(--text); font: 15px/1.5 var(--sans); }
main { max-width: 1180px; margin: 0 auto; padding: 32px 16px 64px; }
a { color: var(--accent); }
h1, h2, h3 { line-height: 1.2; margin: 0; }
h1 { font-size: clamp(26px, 4.4vw, 40px); letter-spacing: -0.02em; font-weight: 700; max-width: 22ch; }
h2 { font-size: 20px; letter-spacing: -0.01em; }
h3 { font-size: 15px; }
p { margin: 0; }
.mono, code, pre { font-family: var(--mono); }
.brand { font: 600 13px/1 var(--mono); color: var(--muted); letter-spacing: 0.02em; margin-bottom: 18px; display: flex; gap: 8px; align-items: center; }
.brand i { width: 10px; height: 10px; border-radius: 2px; background: var(--accent); display: inline-block; }
.subhead { color: var(--muted); font-size: 17px; margin-top: 12px; max-width: 70ch; }
.meta { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 1px; margin: 24px 0 0; background: var(--line); border: 1px solid var(--line); border-radius: 10px; overflow: hidden; }
.meta div { background: var(--surface); padding: 10px 14px; min-width: 0; }
.meta dt { font-size: 12px; color: var(--faint); }
.meta dd { margin: 2px 0 0; font-size: 14px; overflow-wrap: anywhere; }
.banner { margin-top: 16px; padding: 10px 14px; border-radius: 8px; background: var(--beyond-soft); color: var(--text); border: 1px solid var(--line); }
section { margin-top: 40px; }
.section-head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 8px 16px; margin-bottom: 12px; }
.section-head p { color: var(--muted); font-size: 14px; max-width: 70ch; }
.card { background: var(--surface); border: 1px solid var(--line); border-radius: 12px; }
.scroll { overflow-x: auto; -webkit-overflow-scrolling: touch; }
table.summary { border-collapse: separate; border-spacing: 0; width: 100%; min-width: 640px; font-variant-numeric: tabular-nums; }
.summary th, .summary td { text-align: left; vertical-align: top; padding: 10px 12px; border-bottom: 1px solid var(--line); }
.summary thead th { font-weight: 600; font-size: 13px; background: var(--well); position: sticky; top: 0; }
.summary thead th:first-child { border-top-left-radius: 12px; }
.summary thead th:last-child { border-top-right-radius: 12px; }
.summary tbody tr:last-child td, .summary tbody tr:last-child th { border-bottom: 0; }
.summary tbody th { font-weight: 500; width: 22%; min-width: 150px; }
.summary .group th { background: var(--bg); color: var(--faint); font: 600 12px/1 var(--sans); text-transform: uppercase; letter-spacing: 0.06em; padding: 14px 12px 8px; }
.summary .sub { display: block; color: var(--faint); font-size: 12px; font-weight: 400; margin-top: 2px; }
.summary .model { display: block; font: 12px/1.4 var(--mono); color: var(--muted); font-weight: 400; overflow-wrap: anywhere; }
.summary .value { font-weight: 600; font-size: 15px; }
.summary .delta { display: block; font-size: 13px; color: var(--muted); }
.summary .noise { display: block; font-size: 12px; color: var(--floor); }
.summary td.floorcol { color: var(--floor); font-size: 13px; background: color-mix(in srgb, var(--floor) 6%, transparent); }
.summary th.floorcol { color: var(--floor); }
.summary td.prod { color: var(--muted); }
.pill { display: inline-block; margin-top: 4px; padding: 1px 8px; border-radius: 999px; font-size: 12px; line-height: 18px; border: 1px solid transparent; }
.pill.within { background: var(--within-soft); color: var(--within); }
.pill.beyond { background: var(--beyond-soft); color: var(--beyond); }
.pill.none { background: var(--well); color: var(--faint); }
.notes { margin: 10px 2px 0; padding: 0; list-style: none; color: var(--muted); font-size: 13px; }
.notes li + li { margin-top: 4px; }
.sentences { margin-top: 16px; padding: 14px 16px; }
.sentences h3 { margin-bottom: 8px; }
.sentences dl { margin: 0; }
.sentences dt { font: 600 13px/1.4 var(--mono); margin-top: 10px; }
.sentences dt:first-child { margin-top: 0; }
.sentences dd { margin: 2px 0 0; color: var(--muted); font-size: 14px; }
.chart { padding: 16px; }
.chart-controls { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 8px; font-size: 14px; }
select, button { font: inherit; color: inherit; }
select { background: var(--surface); border: 1px solid var(--line-strong); border-radius: 6px; padding: 4px 8px; }
svg.frontier { width: 100%; height: auto; display: block; }
svg.frontier text { fill: var(--muted); font: 12px var(--sans); }
svg.frontier .axis { stroke: var(--line-strong); }
svg.frontier .grid { stroke: var(--line); }
svg.frontier .whisker { stroke: var(--faint); stroke-width: 1.2; }
svg.frontier .pt { fill: var(--surface); stroke: var(--text); stroke-width: 2; }
svg.frontier .pt.on { fill: var(--accent); stroke: var(--accent); }
svg.frontier .pt.control { fill: var(--floor); stroke: var(--floor); }
svg.frontier .front { stroke: var(--accent); stroke-width: 1.5; fill: none; stroke-dasharray: 4 3; }
svg.frontier .floorline { stroke: var(--floor); stroke-width: 1.2; stroke-dasharray: 2 3; }
svg.frontier text.floorlabel { fill: var(--floor); }
svg.frontier text.ptlabel { fill: var(--text); font-weight: 600; }
.chart-note { color: var(--muted); font-size: 13px; margin-top: 8px; }
.floor-panel { padding: 18px 20px; border-left: 4px solid var(--floor); }
.floor-panel p + p { margin-top: 8px; }
.floor-panel .facts { color: var(--muted); font-size: 13px; margin-top: 10px; }
.filters { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; font-size: 14px; }
.seg { display: inline-flex; border: 1px solid var(--line-strong); border-radius: 8px; overflow: hidden; }
.seg button { background: var(--surface); border: 0; padding: 6px 12px; cursor: pointer; }
.seg button + button { border-left: 1px solid var(--line-strong); }
.seg button[aria-pressed="true"] { background: var(--text); color: var(--bg); }
.count { color: var(--faint); }
.cases { display: grid; gap: 8px; margin-top: 12px; }
details.case { background: var(--surface); border: 1px solid var(--line); border-radius: 10px; }
details.case > summary { list-style: none; cursor: pointer; padding: 10px 14px; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 12px; align-items: center; }
details.case > summary::-webkit-details-marker { display: none; }
details.case > summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: 10px; }
.case-title { min-width: 0; }
.case-id { font: 12px var(--mono); color: var(--faint); }
.case-preview { display: block; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.badges { display: flex; flex-wrap: wrap; gap: 4px; justify-content: flex-end; }
.badge { font-size: 12px; padding: 1px 8px; border-radius: 999px; background: var(--well); color: var(--muted); white-space: nowrap; }
.badge.differs { background: var(--beyond-soft); color: var(--beyond); }
.badge.failed { background: var(--danger-soft); color: var(--danger); }
.case-body { padding: 0 14px 14px; border-top: 1px solid var(--line); }
.case-input { margin-top: 12px; padding: 10px 12px; background: var(--well); border-radius: 8px; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 14px; }
.outputs { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 10px; margin-top: 12px; }
.out { border: 1px solid var(--line); border-radius: 8px; min-width: 0; display: flex; flex-direction: column; }
.out.differs { border-color: color-mix(in srgb, var(--beyond) 45%, var(--line)); }
.out.failed { border-color: color-mix(in srgb, var(--danger) 45%, var(--line)); }
.out-head { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 8px; padding: 8px 10px; border-bottom: 1px solid var(--line); font-size: 13px; }
.out-head b { font-weight: 600; }
.out-head span { color: var(--faint); }
.out pre { margin: 0; padding: 10px; font-size: 12.5px; line-height: 1.45; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 320px; overflow: auto; }
.out .flags { padding: 6px 10px 0; font-size: 12px; color: var(--beyond); }
.out .err { padding: 8px 10px; font-size: 13px; color: var(--danger); overflow-wrap: anywhere; }
.floor-outs { margin-top: 10px; }
.floor-outs > summary { cursor: pointer; color: var(--floor); font-size: 13px; }
.limit { color: var(--faint); font-size: 12px; margin-top: 8px; }
.more { margin-top: 12px; }
.more button, .copy { background: var(--surface); border: 1px solid var(--line-strong); border-radius: 8px; padding: 6px 12px; cursor: pointer; }
.reasons { display: grid; gap: 1px; background: var(--line); border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
.reasons div { background: var(--surface); padding: 12px 16px; display: grid; grid-template-columns: 3.5em minmax(0, 1fr); gap: 2px 12px; }
.reasons .n { font: 600 20px/1.2 var(--sans); font-variant-numeric: tabular-nums; grid-row: span 2; }
.reasons p { color: var(--muted); font-size: 14px; }
.cta { margin-top: 48px; padding: 28px; border-radius: 14px; background: var(--text); color: var(--bg); display: grid; grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr); gap: 24px; align-items: center; }
.cta h2 { font-size: 24px; }
.cta p { margin-top: 10px; opacity: 0.82; max-width: 60ch; }
.cta > div { min-width: 0; }
.cta .actions { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
.cta a.button { display: inline-block; text-align: center; background: var(--accent); color: var(--accent-ink); text-decoration: none; font-weight: 600; padding: 12px 18px; border-radius: 10px; }
.cta a.button:focus-visible { outline: 2px solid var(--bg); outline-offset: 3px; }
.cta .agent { font-size: 13px; opacity: 0.82; }
.cta .cmd { display: flex; gap: 8px; align-items: stretch; margin-top: 6px; }
.cta code { flex: 1; min-width: 0; padding: 8px 10px; border-radius: 8px; background: color-mix(in srgb, var(--bg) 12%, transparent); color: var(--bg); font-size: 13px; overflow-x: auto; white-space: nowrap; }
.cta .copy { background: transparent; color: var(--bg); border-color: color-mix(in srgb, var(--bg) 35%, transparent); }
footer { margin-top: 28px; color: var(--faint); font-size: 13px; display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px; }
.sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
.noscript { padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: 8px; }
@media (max-width: 720px) {
  main { padding-top: 20px; }
  .cta { grid-template-columns: minmax(0, 1fr); padding: 20px; }
  details.case > summary { grid-template-columns: 1fr; }
  .badges { justify-content: flex-start; }
}
@media print {
  body { background: #fff; }
  details.case { break-inside: avoid; }
}
`;

// The page's script. It reads the JSON block and builds every element with
// createElement and textContent; nothing captured reaches innerHTML.
const SCRIPT = `
(function () {
  "use strict";
  var data = JSON.parse(document.getElementById("report-data").textContent);
  var app = document.getElementById("app");
  var SVG = "http://www.w3.org/2000/svg";

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) for (var k in attrs) {
      if (attrs[k] == null || attrs[k] === false) continue;
      if (k === "text") node.textContent = attrs[k];
      else if (k === "class") node.className = attrs[k];
      else node.setAttribute(k, attrs[k]);
    }
    (children || []).forEach(function (c) { if (c != null) node.appendChild(typeof c === "string" ? document.createTextNode(c) : c); });
    return node;
  }
  function svg(tag, attrs, text) {
    var node = document.createElementNS(SVG, tag);
    for (var k in attrs) node.setAttribute(k, attrs[k]);
    if (text != null) node.textContent = text;
    return node;
  }
  function section(id, title, intro, body) {
    var head = el("div", { class: "section-head" }, [el("h2", { id: id + "-title", text: title }), intro ? el("p", { text: intro }) : null]);
    return el("section", { id: id, "aria-labelledby": id + "-title" }, [head].concat(body));
  }

  // --- Header
  var meta = el("dl", { class: "meta" }, data.meta.map(function (m) {
    return el("div", null, [el("dt", { text: m.label }), el("dd", { text: m.value })]);
  }));
  app.appendChild(el("header", null, [
    el("div", { class: "brand" }, [el("i", { "aria-hidden": "true" }), "metergraph replay"]),
    el("h1", { text: data.headline }),
    el("p", { class: "subhead", text: data.subhead }),
    meta,
    data.stopped ? el("p", { class: "banner", text: data.stopped }) : null
  ]));

  // --- Summary table
  function th(text, attrs) { return el("th", Object.assign({ scope: "col", text: text }, attrs || {})); }
  var headRow = el("tr", null, [th("Metric")]);
  headRow.appendChild(el("th", { scope: "col" }, ["Production", el("span", { class: "model", text: data.productionModel + ", the control" })]));
  data.columns.forEach(function (c) {
    headRow.appendChild(el("th", { scope: "col" }, [c.label, el("span", { class: "model", text: c.model }), el("span", { class: "sub", text: c.settings })]));
  });
  if (data.hasFloor) headRow.appendChild(el("th", { scope: "col", class: "floorcol" }, ["Production vs itself", el("span", { class: "sub", text: "A2 against A1" })]));
  var tbody = el("tbody");
  var span = 2 + data.columns.length + (data.hasFloor ? 1 : 0);
  var groups = { monthly_cost_usd: "Cost and speed", output_chars_p50: "Output shape" };
  var fieldGroupShown = false;
  data.rows.forEach(function (row) {
    var group = groups[row.key];
    if (row.kind === "field" && !fieldGroupShown) { group = "Tracked fields, agreement with production"; fieldGroupShown = true; }
    if (group) tbody.appendChild(el("tr", { class: "group" }, [el("th", { colspan: span, scope: "colgroup", text: group })]));
    var tr = el("tr", null, [el("th", { scope: "row", class: row.kind === "field" ? "mono" : null }, [row.label, row.sublabel ? el("span", { class: "sub", text: row.sublabel }) : null])]);
    tr.appendChild(el("td", { class: "prod" }, [el("span", { class: "value", text: row.production }), row.productionNote ? el("span", { class: "delta", text: row.productionNote }) : null]));
    row.cells.forEach(function (cell, i) {
      var parts = [el("span", { class: "value", text: cell.text })];
      if (cell.delta) parts.push(el("span", { class: "delta", text: cell.delta === "same" ? "same as production" : cell.delta }));
      if (cell.floor) parts.push(el("span", { class: "noise", text: "production vs itself: " + cell.floor }));
      if (cell.label) parts.push(el("span", { class: "pill " + cell.label.tone, title: cell.label.title, text: cell.label.text }));
      var title = cell.title || (row.sentences ? row.sentences[i] : null);
      tr.appendChild(el("td", { title: title }, parts));
    });
    if (data.hasFloor) tr.appendChild(el("td", { class: "floorcol", text: row.floor || "—" }));
    tbody.appendChild(tr);
  });
  var table = el("table", { class: "summary" }, [el("caption", { class: "sr", text: "Each treatment against production, with production's gap from itself" }), el("thead", null, [headRow]), tbody]);
  var notes = [];
  data.rows.forEach(function (r) { if (r.note) notes.push(r.note); });
  data.columns.forEach(function (c) { if (c.note) notes.push(c.label + ": " + c.note); });
  if (data.hasFloor) notes.push("Cost and latency are read against production's model replayed through the same endpoint (A1), because production's recorded latency and cost were not measured the way a replay is.");
  if (data.labels) notes.push("Within noise: the treatment's gap from production is no larger than production's gap from itself on the same calls. Beyond noise: it is larger. Neither says the treatment is better, worse or safe to switch to.");
  var summaryBody = [el("div", { class: "card scroll" }, [table]), notes.length ? el("ul", { class: "notes" }, notes.map(function (n) { return el("li", { text: n }); })) : null];
  var fieldRows = data.rows.filter(function (r) { return r.kind === "field" && r.sentences; });
  if (fieldRows.length) {
    var dl = el("dl");
    fieldRows.forEach(function (r) {
      dl.appendChild(el("dt", { text: r.label }));
      r.sentences.forEach(function (s) { dl.appendChild(el("dd", { text: s })); });
    });
    summaryBody.push(el("div", { class: "card sentences" }, [el("h3", { text: "Tracked fields in words" }), dl]));
  }
  app.appendChild(section("summary", "What changed", "Each treatment against production's recorded output, with production's own replay-to-replay gap beside it. These are statistics from a sample, not a verdict.", summaryBody));

  // --- Frontier chart
  if (data.charts.length) {
    var holder = el("div");
    var controls = el("div", { class: "chart-controls" });
    var select = null;
    if (data.charts.length > 1) {
      select = el("select", { id: "metric", "aria-label": "Metric drawn up the chart" }, data.charts.map(function (c, i) { return el("option", { value: String(i), text: c.label }); }));
      controls.appendChild(el("label", { for: "metric", text: "Up the chart:" }));
      controls.appendChild(select);
    } else {
      controls.appendChild(el("span", { text: "Up the chart: " + data.charts[0].label }));
    }
    var note = el("p", { class: "chart-note" });
    var draw = function (chart) {
      while (holder.firstChild) holder.removeChild(holder.firstChild);
      holder.appendChild(frontierSvg(chart));
      var text = "Cost per call across, " + chart.label + " up. Whiskers are 90% bootstrap ranges." + (chart.points.some(function (p) { return p.control; }) ? " The purple point is production's model replayed." : "") + (chart.floor ? " The dotted line: " + chart.floor.text + "." : "");
      if (chart.frontier.length) text += " The dashed line joins the points no other point is both cheaper than and " + (chart.better === "higher" ? "higher" : "lower") + " on: " + chart.frontier.join(", ") + ". A fact about these points, not a recommendation.";
      note.textContent = text;
    };
    var current = data.charts[0];
    if (select) select.addEventListener("change", function () { current = data.charts[Number(select.value)]; draw(current); });
    var lastWidth = 0;
    window.addEventListener("resize", function () { if (Math.abs(holder.clientWidth - lastWidth) > 40) { lastWidth = holder.clientWidth; draw(current); } });
    app.appendChild(section("frontier", "Cost against quality", null, [el("div", { class: "card chart" }, [controls, holder, note])]));
    lastWidth = holder.clientWidth;
    draw(current);
  }

  function fmt(format, v) {
    if (format === "usd") { if (v === 0) return "$0"; if (Math.abs(v) >= 1) return "$" + v.toFixed(2); if (Math.abs(v) >= 0.01) return "$" + v.toFixed(3); return "$" + Number(v.toPrecision(2)); }
    if (format === "ms") return v >= 1000 ? (v / 1000).toFixed(1) + " s" : Math.round(v) + " ms";
    if (format === "percent") return Math.round(v * 100) + "%";
    return (v > 0 ? "+" : v < 0 ? "\\u2212" : "") + Math.abs(v).toFixed(Math.abs(v) >= 10 ? 0 : 2);
  }
  function frontierSvg(chart) {
    var W = Math.max(320, Math.min(760, Math.round(holder.clientWidth || 720))), H = Math.round(Math.max(260, Math.min(380, W * 0.55))), L = 58, R = 16, T = 20, B = 48;
    var pts = chart.points;
    var xs = [0], ys = [];
    pts.forEach(function (p) { xs.push(p.x, p.xHigh == null ? p.x : p.xHigh); ys.push(p.y, p.yLow == null ? p.y : p.yLow, p.yHigh == null ? p.y : p.yHigh); });
    if (chart.floor) ys.push(chart.floor.y);
    var xMax = Math.max.apply(null, xs) * 1.08 || 1;
    var yMin = Math.min.apply(null, ys), yMax = Math.max.apply(null, ys);
    if (chart.format === "percent") { yMin = Math.min(yMin, 0); yMax = Math.max(yMax, 1); }
    else if (chart.format === "ms") { yMin = 0; yMax = yMax * 1.08 || 1; }
    else { var pad = (yMax - yMin) * 0.1 || 1; yMin -= pad; yMax += pad; }
    var sx = function (v) { return L + (v / xMax) * (W - L - R); };
    var sy = function (v) { return T + (1 - (v - yMin) / (yMax - yMin || 1)) * (H - T - B); };
    var root = svg("svg", { class: "frontier", viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": "Cost per call against " + chart.label });
    var ticks = W < 520 ? 2 : 4;
    for (var i = 0; i <= 4; i++) {
      var yv = yMin + (yMax - yMin) * i / 4, xv = xMax * i / 4;
      if (ticks === 2 && i % 2) { root.appendChild(svg("line", { class: "grid", x1: L, x2: W - R, y1: sy(yv), y2: sy(yv) })); root.appendChild(svg("text", { x: L - 8, y: sy(yv) + 4, "text-anchor": "end" }, fmt(chart.format, yv))); continue; }
      root.appendChild(svg("line", { class: "grid", x1: L, x2: W - R, y1: sy(yv), y2: sy(yv) }));
      root.appendChild(svg("text", { x: L - 8, y: sy(yv) + 4, "text-anchor": "end" }, fmt(chart.format, yv)));
      root.appendChild(svg("text", { x: sx(xv), y: H - B + 18, "text-anchor": i === 0 ? "start" : i === 4 ? "end" : "middle" }, fmt("usd", xv)));
    }
    root.appendChild(svg("line", { class: "axis", x1: L, x2: W - R, y1: H - B, y2: H - B }));
    root.appendChild(svg("line", { class: "axis", x1: L, x2: L, y1: T, y2: H - B }));
    root.appendChild(svg("text", { x: (L + W - R) / 2, y: H - 8, "text-anchor": "middle" }, "Cost per call"));
    if (chart.floor) {
      root.appendChild(svg("line", { class: "floorline", x1: L, x2: W - R, y1: sy(chart.floor.y), y2: sy(chart.floor.y) }));
    }
    var front = pts.filter(function (p) { return p.onFrontier; }).sort(function (a, b) { return a.x - b.x; });
    if (front.length > 1) root.appendChild(svg("polyline", { class: "front", points: front.map(function (p) { return sx(p.x) + "," + sy(p.y); }).join(" ") }));
    var placed = [];
    function labelAt(p) {
      var w = p.label.length * 7 + 4, cx = sx(p.x), cy = sy(p.y);
      var tries = [[10, -8, "start"], [10, 18, "start"], [-10, -8, "end"], [-10, 18, "end"]];
      for (var t = 0; t < tries.length; t++) {
        var x = cx + tries[t][0], y = cy + tries[t][1], anchor = tries[t][2];
        var x0 = anchor === "start" ? x : x - w, box = [x0, y - 12, x0 + w, y + 2];
        if (box[0] < L || box[2] > W - R + 4) continue;
        var hit = placed.some(function (b) { return box[0] < b[2] && b[0] < box[2] && box[1] < b[3] && b[1] < box[3]; });
        if (!hit) { placed.push(box); return { x: x, y: y, anchor: anchor }; }
      }
      return { x: cx + 10, y: cy - 8, anchor: "start" };
    }
    pts.forEach(function (p) {
      var g = svg("g", {});
      if (p.xLow != null) g.appendChild(svg("line", { class: "whisker", x1: sx(p.xLow), x2: sx(p.xHigh), y1: sy(p.y), y2: sy(p.y) }));
      if (p.yLow != null) g.appendChild(svg("line", { class: "whisker", x1: sx(p.x), x2: sx(p.x), y1: sy(p.yLow), y2: sy(p.yHigh) }));
      g.appendChild(svg("circle", { class: "pt" + (p.onFrontier ? " on" : "") + (p.control ? " control" : ""), cx: sx(p.x), cy: sy(p.y), r: 6 }));
      var at = labelAt(p);
      g.appendChild(svg("text", { class: "ptlabel", x: at.x, y: at.y, "text-anchor": at.anchor }, p.label));
      g.appendChild(svg("title", {}, p.label + ": " + p.xText + " a call, " + p.yText + " (" + p.n + " calls)"));
      root.appendChild(g);
    });
    return root;
  }

  // --- Noise floor
  if (data.floor) {
    var f = data.floor;
    app.appendChild(section("noise-floor", "The noise floor", null, [el("div", { class: "card floor-panel" }, [
      el("p", { text: f.text[0] }),
      el("p", { text: f.text[1] }),
      el("p", { class: "facts" }, ["Measured on " + f.cases + " calls where both replays came back, for " + f.spent + ". ", el("a", { href: data.links.noiseFloor, rel: "noopener noreferrer", text: "How the noise floor works" })])
    ])]));
  } else {
    app.appendChild(section("noise-floor", "The noise floor", null, [el("div", { class: "card floor-panel" }, [
      el("p", { text: "This run did not measure a noise floor, so differences are shown without production's own variation beside them. Run again with the noise floor on to see how much production differs from itself." }),
      el("p", { class: "facts" }, [el("a", { href: data.links.noiseFloor, rel: "noopener noreferrer", text: "How the noise floor works" })])
    ])]));
  }

  // --- Case browser
  var PAGE = 30;
  var filter = "all", shown = PAGE;
  var list = el("div", { class: "cases" });
  var count = el("span", { class: "count", "aria-live": "polite" });
  var more = el("div", { class: "more" });
  function outputBox(o, isFloor) {
    var cls = "out" + (o.differs ? " differs" : "") + (o.status === "failed" ? " failed" : "");
    var head = el("div", { class: "out-head" }, [el("b", { text: o.label }), el("span", { text: o.latency + " \\u00b7 " + o.cost })]);
    var kids = [head];
    if (o.differs && !isFloor) kids.push(el("div", { class: "flags", text: "differs from production" }));
    if (o.flags.length) kids.push(el("div", { class: "flags", text: o.flags.join(", ") }));
    if (o.error) kids.push(el("div", { class: "err", text: o.error }));
    if (o.output != null) kids.push(el("pre", { text: o.pretty != null ? o.pretty : o.output }));
    else if (!o.error) kids.push(el("div", { class: "err", text: "No output" }));
    return el("div", { class: cls }, kids);
  }
  function caseCard(c) {
    var badges = el("div", { class: "badges" });
    if (c.status === "not_replayable") badges.appendChild(el("span", { class: "badge", text: "not replayable: " + (c.reason || "unknown") }));
    c.arms.forEach(function (a) {
      if (a.status === "failed") badges.appendChild(el("span", { class: "badge failed", text: a.label + " failed" }));
      else if (a.differs) badges.appendChild(el("span", { class: "badge differs", text: a.label + " differs" }));
    });
    var summary = el("summary", null, [
      el("div", { class: "case-title" }, [el("span", { class: "case-id", text: c.id + (c.traceId && c.traceId !== c.id ? " \\u00b7 " + c.traceId : "") }), el("span", { class: "case-preview", text: c.input || "(no input preview)" })]),
      badges
    ]);
    var body = el("div", { class: "case-body" });
    var built = false;
    var details = el("details", { class: "case" }, [summary, body]);
    details.addEventListener("toggle", function () {
      if (!details.open || built) return;
      built = true;
      body.appendChild(el("div", { class: "case-input", text: c.input || "(no input preview)" }));
      var outs = el("div", { class: "outputs" }, [outputBox(c.production, true)].concat(c.arms.map(function (a) { return outputBox(a, false); })));
      body.appendChild(outs);
      if (c.floor.length) body.appendChild(el("details", { class: "floor-outs" }, [el("summary", { text: "Production's model replayed twice (the noise floor)" }), el("div", { class: "outputs" }, c.floor.map(function (o) { return outputBox(o, true); }))]));
      if (c.limitations.length) body.appendChild(el("p", { class: "limit", text: "Not carried by the trace: " + c.limitations.join(", ") }));
    });
    return details;
  }
  function renderCases() {
    var items = data.cases.filter(function (c) { return filter === "all" || c.differs; });
    while (list.firstChild) list.removeChild(list.firstChild);
    items.slice(0, shown).forEach(function (c) { list.appendChild(caseCard(c)); });
    count.textContent = items.length + " of " + data.cases.length + " calls";
    while (more.firstChild) more.removeChild(more.firstChild);
    if (items.length > shown) {
      var b = el("button", { type: "button", text: "Show " + Math.min(PAGE, items.length - shown) + " more" });
      b.addEventListener("click", function () { shown += PAGE; renderCases(); });
      more.appendChild(b);
    }
  }
  var seg = el("div", { class: "seg", role: "group", "aria-label": "Which calls to show" });
  [["all", "All calls"], ["differs", "Differs from production"]].forEach(function (opt) {
    var b = el("button", { type: "button", "aria-pressed": opt[0] === filter ? "true" : "false", text: opt[1] });
    b.addEventListener("click", function () {
      filter = opt[0]; shown = PAGE;
      Array.prototype.forEach.call(seg.children, function (x) { x.setAttribute("aria-pressed", x === b ? "true" : "false"); });
      renderCases();
    });
    seg.appendChild(b);
  });
  renderCases();
  app.appendChild(section("cases", "Calls", "Each call's input, production's output and every treatment's output side by side. A treatment differs when a tracked field's value changed, or, with no tracked fields, when its text did; a failure or refusal also counts.", [el("div", { class: "filters" }, [seg, count]), list, more]));

  // --- Not replayable
  if (data.notReplayable.length) {
    var total = data.notReplayable.reduce(function (s, r) { return s + r.count; }, 0);
    app.appendChild(section("not-replayable", "Not replayed", total + " sampled " + (total === 1 ? "call" : "calls") + " could not be replayed faithfully, so " + (total === 1 ? "it is" : "they are") + " left out of every figure above.", [
      el("div", { class: "reasons" }, data.notReplayable.map(function (r) {
        return el("div", null, [el("span", { class: "n", text: String(r.count) }), el("h3", { text: r.title }), el("p", { text: r.fix })]);
      }))
    ]));
  }

  // --- Call to action
  var copy = el("button", { type: "button", class: "copy", text: "Copy" });
  var code = el("code", { text: data.links.setup });
  copy.addEventListener("click", function () {
    var done = function () { copy.textContent = "Copied"; setTimeout(function () { copy.textContent = "Copy"; }, 1600); };
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(data.links.setup).then(done, select);
    else select();
    function select() { var r = document.createRange(); r.selectNodeContents(code); var s = window.getSelection(); s.removeAllRanges(); s.addRange(r); }
  });
  app.appendChild(el("section", { class: "cta", id: "hosted", "aria-labelledby": "hosted-title" }, [
    el("div", null, [
      el("h2", { id: "hosted-title", text: "Keep this running on live traffic" }),
      el("p", { text: "This report is one sample, replayed once. Hosted Metergraph replays new production traffic continuously, grades quality with a judge beside these statistics, and finds which of your workloads are worth testing." }),
      el("p", { text: "Share results with your team, then roll out a switch behind a quality gate. The link starts hosted setup from this run's models, settings and field names. It never carries your traces or outputs." })
    ]),
    el("div", { class: "actions" }, [
      el("a", { class: "button", href: data.links.hosted, rel: "noopener", text: "Set up continuous replay" }),
      el("div", { class: "agent" }, ["Or let your coding agent set it up:", el("div", { class: "cmd" }, [code, copy])])
    ])
  ]));

  app.appendChild(el("footer", null, [
    el("span", { text: data.tool.name + " " + data.tool.version + ". Statistics only: nothing here says a model is good enough to switch to." }),
    el("span", { text: "Generated locally. This file makes no network requests." })
  ]));
})();
`;
