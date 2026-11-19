(function () {
  "use strict";

  var DATA = JSON.parse(document.getElementById("replay-data").textContent);
  var app = document.getElementById("app");
  var LABELS = {
    beyond_noise: "beyond noise",
    within_noise: "within noise",
    too_few_calls: "too few calls",
    not_comparable: "not comparable"
  };

  // el builds an element. Text always goes in as text, never as markup.
  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === null || v === undefined || v === false) return;
        if (k === "class") node.className = v;
        else if (k === "text") node.textContent = v;
        else if (k.slice(0, 2) === "on") node.addEventListener(k.slice(2), v);
        else node.setAttribute(k, v === true ? "" : v);
      });
    }
    (children || []).forEach(function (c) {
      if (c === null || c === undefined || c === false) return;
      node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    });
    return node;
  }

  function pct(v) {
    if (v === null || v === undefined || isNaN(v)) return "—";
    var r = Math.round(v * 100);
    return (r > 0 ? "+" : r < 0 ? "−" : "") + Math.abs(r) + "%";
  }

  function usd(v) {
    if (v === null || v === undefined) return "—";
    if (v === 0) return "$0";
    if (v < 0.0001) return "$" + v.toPrecision(2);
    if (v < 0.01) return "$" + v.toFixed(4);
    return "$" + v.toFixed(2);
  }

  function ms(v) {
    if (v === null || v === undefined) return "";
    return v < 1000 ? Math.round(v) + " ms" : (v / 1000).toFixed(1) + " s";
  }

  function plural(n, one, many) { return n === 1 ? one : many; }

  function themeButton() {
    var b = el("button", { class: "theme", type: "button", text: "Theme" });
    b.addEventListener("click", function () {
      var root = document.documentElement;
      var dark = root.getAttribute("data-theme")
        ? root.getAttribute("data-theme") === "dark"
        : window.matchMedia("(prefers-color-scheme: dark)").matches;
      var next = dark ? "light" : "dark";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("replay-theme", next); } catch (e) { /* no storage */ }
    });
    return b;
  }

  try {
    var saved = localStorage.getItem("replay-theme");
    if (saved === "light" || saved === "dark") document.documentElement.setAttribute("data-theme", saved);
  } catch (e) { /* no storage */ }

  // ---------------------------------------------------------------------
  // JSON values, read as the map reads them (approximately: the map's own
  // verdict per case comes from the run, these only mark fields).

  function parse(text) {
    if (text === null || text === undefined) return { ok: false };
    var t = String(text).trim();
    var fence = /^```(?:json)?\s*([\s\S]*?)\s*```$/.exec(t);
    if (fence) t = fence[1];
    try { return { ok: true, value: JSON.parse(t) }; } catch (e) { return { ok: false }; }
  }

  function at(value, path) {
    var steps = path.replace(/^\$\.?/, "").split(".").filter(Boolean);
    var current = [value];
    var many = false;
    steps.forEach(function (step) {
      var list = /\[\]$/.test(step);
      var key = step.replace(/\[\]$/, "");
      var next = [];
      current.forEach(function (v) {
        if (v === null || typeof v !== "object" || Array.isArray(v)) return;
        if (!(key in v)) return;
        var got = v[key];
        if (list) {
          many = true;
          if (Array.isArray(got)) got.forEach(function (x) { next.push(x); });
        } else next.push(got);
      });
      current = next;
    });
    if (many) return current.length ? { present: true, value: current } : { present: false };
    if (!current.length) return { present: false };
    // A key held as null is the answer "none": compared, but no value.
    if (current[0] === null) return { present: false, answered: true };
    return { present: true, value: current[0] };
  }

  function canon(v) {
    if (Array.isArray(v)) return "[" + v.map(canon).join(",") + "]";
    if (v && typeof v === "object") {
      return "{" + Object.keys(v).sort().map(function (k) { return JSON.stringify(k) + ":" + canon(v[k]); }).join(",") + "}";
    }
    if (typeof v === "string") return JSON.stringify(v.trim());
    return JSON.stringify(v);
  }

  function show(r) {
    if (!r.present) return "(no value)";
    return typeof r.value === "string" ? r.value : JSON.stringify(r.value);
  }

  function unreadable(reply) {
    if (!reply) return "no reply";
    if (reply.status !== "ok") return reply.status === "failed" ? "failed" : "not sent";
    if (reply.refusal) return "refused";
    if (reply.finishReason === "length" || reply.finishReason === "max_tokens") return "cut off";
    return "";
  }

  // ---------------------------------------------------------------------
  // Map

  function renderMap() {
    var parts = {};
    DATA.parts.forEach(function (p) { parts[p.name] = p; });
    var cases = {};
    (DATA.cases || []).forEach(function (c) { (cases[c.part] = cases[c.part] || []).push(c); });
    var order = DATA.summary.order;
    var selected = null;

    app.appendChild(themeButton());
    app.appendChild(el("h1", { text: "Where outputs change" }));
    var meta = [
      [DATA.parts.length + " " + plural(DATA.parts.length, "part", "parts"), ""],
      [DATA.file, "trace file "],
      [DATA.from ? (DATA.from === DATA.to ? DATA.from : DATA.from + " to " + DATA.to) : "", "recorded "],
      [String(DATA.perPart), "cases per part "],
      [usd(DATA.spentUsd) + " of " + usd(DATA.budgetUsd), "spent "]
    ];
    app.appendChild(el("div", { class: "meta" }, meta.filter(function (m) { return m[0]; }).map(function (m) {
      return el("span", null, [m[1], el("b", { text: m[0] })]);
    })));
    if (DATA.stopped) {
      app.appendChild(el("p", { class: "card", text: "The run stopped early (" + DATA.stopped.replace(/_/g, " ") + "). Parts are labelled from the cases that finished." }));
    }

    var summaryCard = el("section", { class: "card" });
    summaryCard.appendChild(el("h2", { text: "Each model on each part" }));
    summaryCard.appendChild(el("p", { class: "muted", text: "Choose a cell to see the cases behind it." }));
    var head = el("tr", null, [el("th", { text: "Model" })].concat(order.map(function (name) {
      return el("th", { class: "part", text: name });
    })).concat([el("th", { class: "num", text: "Cost per call" })]));
    var body = el("tbody");
    var buttons = [];
    DATA.treatments.forEach(function (t, ti) {
      var row = el("tr", null, [el("td", { class: "model" }, [el("span", { class: "letter", text: t.letter }), t.model,
        t.settings ? el("div", { class: "faint", text: t.settings }) : null])]);
      order.forEach(function (name) {
        var mt = parts[name].treatments[ti];
        var count = "";
        if (mt.result === "beyond_noise" || mt.result === "within_noise") count = mt.changed + " of " + mt.paired + " changed";
        else if (mt.reason) count = mt.reason;
        var b = el("button", { type: "button", class: "cell " + mt.result, "aria-pressed": "false",
          title: LABELS[mt.result] + (count ? ", " + count : "") },
          [el("span", { class: "label" }, [LABELS[mt.result], mt.cutOff ? el("span", { class: "tag", text: "cut off" }) : null]),
            el("span", { class: "count", text: count || " " })]);
        b.addEventListener("click", function () { select(ti, name); });
        buttons.push({ b: b, ti: ti, name: name });
        row.appendChild(el("td", null, [b]));
      });
      row.appendChild(el("td", { class: "num", text: pct(DATA.summary.cost[ti]) }));
      body.appendChild(row);
    });
    summaryCard.appendChild(el("div", { class: "scroll" }, [el("table", { class: "matrix" }, [el("thead", null, [head]), body])]));
    var anyCut = DATA.parts.some(function (p) { return p.treatments.some(function (t) { return t.cutOff > 0; }); });
    summaryCard.appendChild(el("div", { class: "legend" }, [
      el("p", { text: "Changed counts the paired cases where the model's answer differs from the recorded one. A part is beyond noise when that exceeds a fresh run of production's own model by a paired sign test at 5%." }),
      el("p", { text: rerunSentence() }),
      el("p", { text: "Cost per call is the mean change across the parts, against production's model replayed." }),
      anyCut ? el("p", { text: "Cut off: some replies hit the output cap. Open the cell to see whether reasoning used it up." }) : null
    ]));
    app.appendChild(summaryCard);

    var detail = el("section", { class: "card", id: "detail" });
    app.appendChild(detail);
    app.appendChild(el("div", { class: "foot" }, [DATA.judgedNote ? el("p", { text: DATA.judgedNote }) : null, el("p", { text: DATA.rule }), el("p", { text: DATA.footer }),
      el("p", { class: "faint", text: "Prices: " + DATA.catalog + ". Made " + DATA.created + " by metergraph-replay." })]));

    function rerunSentence() {
      var most = 0;
      DATA.parts.forEach(function (p) {
        p.treatments.forEach(function (t) {
          if (t.result === "beyond_noise" || t.result === "within_noise") most = Math.max(most, t.rerun);
        });
      });
      return most === 0
        ? "Production's own rerun changed no answers in any part, so every change shown comes from the model."
        : "Production's own rerun changed up to " + most + " answers in a part; the test reads changes beyond that.";
    }

    function select(ti, name) {
      selected = { ti: ti, name: name };
      buttons.forEach(function (x) { x.b.setAttribute("aria-pressed", String(x.ti === ti && x.name === name)); });
      drawDetail();
      if (location.hash !== "#" + ti + "/" + encodeURIComponent(name)) {
        history.replaceState(null, "", "#" + ti + "/" + encodeURIComponent(name));
      }
    }

    function drawDetail() {
      detail.textContent = "";
      var t = DATA.treatments[selected.ti];
      var part = parts[selected.name];
      var mt = part.treatments[selected.ti];
      detail.appendChild(el("div", { class: "detail-head" }, [
        el("h2", { text: selected.name }),
        el("span", { class: "muted", text: t.letter + " " + t.model + " against the recording" })
      ]));
      var figures = [["Label", LABELS[mt.result]]];
      if (mt.result === "beyond_noise" || mt.result === "within_noise") {
        figures.push(["Changed", mt.changed + " of " + mt.paired]);
        figures.push(["Production rerun", mt.rerun + " of " + mt.paired]);
      } else if (mt.reason) figures.push(["Why", mt.reason]);
      figures.push(["Cost per call", pct(mt.costPerCallChange)]);
      figures.push(["Latency", pct(mt.latencyChange)]);
      if (mt.cutOff) figures.push(["Cut off", mt.cutOff + " of " + mt.paired]);
      if (mt.judged) figures.push(["Judge unsure", (mt.unsure || 0) + " of " + mt.paired]);
      if (mt.failed) figures.push(["Failed calls", String(mt.failed)]);
      detail.appendChild(el("div", { class: "figures" }, figures.map(function (f) {
        return el("div", { class: "figure" }, [el("div", { class: "k", text: f[0] }), el("div", { class: "v", text: f[1] })]);
      })));
      if (part.paths && part.paths.length && mt.pathCounts && mt.pathCounts.length) {
        detail.appendChild(el("h3", { text: "Fields compared" }));
        detail.appendChild(el("div", { class: "scroll" }, [el("table", null, [
          el("thead", null, [el("tr", null, [el("th", { text: "Field" }), el("th", { class: "num", text: "Changed" }), el("th", { class: "num", text: "Rerun" })])]),
          el("tbody", null, mt.pathCounts.map(function (pc) {
            return el("tr", null, [el("td", null, [el("code", { text: pc.path.replace(/^\$\./, "") })]),
              el("td", { class: "num", text: String(pc.changed) }), el("td", { class: "num", text: String(pc.rerun) })]);
          }))
        ])]));
      }
      if (mt.notes && mt.notes.length) {
        detail.appendChild(el("ul", { class: "notes" }, mt.notes.map(function (n) { return el("li", { text: n }); })));
      }
      drawCases(part, mt, t);
    }

    function drawCases(part, mt, t) {
      var changed = {};
      (mt.changes || []).forEach(function (ch) { changed[ch.caseId] = ch; });
      var rows = (cases[part.name] || []).map(function (c) {
        var reply = c.arms[t.id];
        var rerun = c.arms.floor_a1;
        var status, kind;
        var problem = unreadable(reply);
        var verdict = (c.judged || {})[t.id];
        if (mt.judged) {
          if (!reply || reply.status !== "ok") { status = problem || "no reply"; kind = "other"; }
          else if (!verdict) { status = "not judged"; kind = "other"; }
          else if (verdict.pDifferent > 0.5) { status = "changed"; kind = "changed"; }
          else { status = "same"; kind = "same"; }
        }
        else if (!part.paths || !part.paths.length) { status = problem || "reply"; kind = problem ? "other" : "same"; }
        else if (reply && reply.status !== "ok") { status = problem; kind = "other"; }
        else if (!rerun || rerun.status !== "ok") { status = "not paired"; kind = "other"; }
        else if (changed[c.id]) { status = "changed"; kind = "changed"; }
        else if (part.paths.every(function (p) { var r = parse(c.recorded.output); if (!r.ok) return true; var got = at(r.value, p); return !got.present && !got.answered; })) { status = "left out"; kind = "other"; }
        else { status = "same"; kind = "same"; }
        return { c: c, status: status, kind: kind };
      });
      var counts = { all: rows.length, changed: 0, same: 0, other: 0 };
      rows.forEach(function (r) { counts[r.kind]++; });
      var filter = counts.changed ? "changed" : "all";
      detail.appendChild(el("h3", { text: "Cases" }));
      if (mt.judged) {
        detail.appendChild(el("p", { class: "muted", text: "This part is free text. " + DATA.judge + " judged each reply against the recorded one, asked in both orders: changed means it found different facts, commitments or next steps." }));
      } else if (!part.paths || !part.paths.length) {
        detail.appendChild(el("p", { class: "muted", text: "This part is free text, so Replay does not compare it. The replies are side by side to read." }));
      }
      var bar = el("div", { class: "filters", role: "group", "aria-label": "Show cases" });
      var list = el("div");
      [["changed", "Changed"], ["same", (part.paths && part.paths.length) || mt.judged ? "Same" : "Replied"], ["other", "Other"], ["all", "All"]].forEach(function (f) {
        if (f[0] !== "all" && !counts[f[0]]) return;
        var b = el("button", { type: "button", "aria-pressed": String(f[0] === filter), text: f[1] + " (" + counts[f[0]] + ")" });
        b.addEventListener("click", function () {
          filter = f[0];
          Array.prototype.forEach.call(bar.children, function (x) { x.setAttribute("aria-pressed", String(x === b)); });
          drawList();
        });
        bar.appendChild(b);
      });
      detail.appendChild(bar);
      detail.appendChild(list);
      drawList();

      function drawList() {
        list.textContent = "";
        var shown = rows.filter(function (r) { return filter === "all" || r.kind === filter; });
        if (!shown.length) { list.appendChild(el("p", { class: "empty", text: "No cases." })); return; }
        shown.forEach(function (r, i) { list.appendChild(caseView(r, part, t, changed[r.c.id], i === 0 && filter === "changed")); });
      }
    }

    function caseView(r, part, t, change, open) {
      var c = r.c;
      var last = c.messages && c.messages.length ? c.messages[c.messages.length - 1].content : "";
      var summary = el("summary", null, [
        el("span", { class: "status " + r.kind, text: r.status }),
        change ? el("span", { class: "mono", text: change.path === "meaning" ? change.to : change.path.replace(/^\$\./, "") + ": " + change.from + " → " + change.to }) : null,
        el("span", { class: "preview", text: last }),
        el("span", { class: "faint mono", text: c.id })
      ]);
      var body = el("div", { class: "case-body" });
      var details = el("details", { class: "case", open: open }, [summary, body]);
      var drawn = false;
      function draw() {
        if (drawn) return;
        drawn = true;
        body.appendChild(el("h3", { text: "Request" }));
        var request = (c.system ? "[system]\n" + c.system + "\n\n" : "") + (c.messages || []).map(function (m) { return "[" + m.role + "]\n" + m.content; }).join("\n\n");
        body.appendChild(el("div", { class: "request", text: request }));
        var arms = [["Recorded", c.recorded], [t.letter + " " + t.model, c.arms[t.id]], ["Production rerun", c.arms.floor_a1]];
        if (part.paths && part.paths.length) body.appendChild(fieldTable(part.paths, arms));
        body.appendChild(el("div", { class: "replies" }, arms.map(function (a) { return replyView(a[0], a[1]); })));
      }
      if (open) draw();
      details.addEventListener("toggle", function () { if (details.open) draw(); });
      return details;
    }

    function fieldTable(paths, arms) {
      var read = arms.map(function (a) { return a[1] && a[1].status === "ok" ? parse(a[1].output) : { ok: false }; });
      var head = el("tr", null, [el("th", { text: "Field" })].concat(arms.map(function (a) { return el("th", { text: a[0] }); })));
      var rows = paths.map(function (p) {
        var base = read[0].ok ? at(read[0].value, p) : { present: false };
        return el("tr", null, [el("td", null, [el("code", { text: p.replace(/^\$\./, "") })])].concat(read.map(function (rd, i) {
          if (!rd.ok) return el("td", { class: "faint", text: unreadable(arms[i][1]) || "reply not JSON" });
          var v = at(rd.value, p);
          var differs = i > 0 && base.present && (v.present !== base.present || canon(v.value) !== canon(base.value));
          return el("td", null, [differs ? el("mark", { text: show(v) }) : show(v)]);
        })));
      });
      return el("div", { class: "scroll" }, [el("table", null, [el("thead", null, [head]), el("tbody", null, rows)])]);
    }

    function replyView(title, reply) {
      var facts = [];
      if (reply && reply.latencyMs) facts.push(ms(reply.latencyMs));
      if (reply && reply.costUsd !== null && reply.costUsd !== undefined) facts.push(usd(reply.costUsd));
      var problem = unreadable(reply);
      var text = reply && reply.output ? reply.output : "";
      var parsed = parse(text);
      if (parsed.ok && typeof parsed.value === "object") text = JSON.stringify(parsed.value, null, 2);
      return el("div", { class: "reply" }, [
        el("h4", null, [title, el("span", { text: facts.join(" · ") })]),
        problem && problem !== "no reply" ? el("div", { class: "problem", text: problem + (reply && reply.error ? ": " + reply.error : "") }) : null,
        text ? el("pre", { text: text }) : el("pre", { class: "empty", text: reply ? "(empty)" : "(no reply)" })
      ]);
    }

    // Open the cell named in the address, else the first beyond-noise cell.
    var fromHash = /^#(\d+)\/(.+)$/.exec(location.hash);
    if (fromHash && DATA.treatments[+fromHash[1]] && parts[decodeURIComponent(fromHash[2])]) {
      select(+fromHash[1], decodeURIComponent(fromHash[2]));
    } else {
      var first = null;
      DATA.treatments.forEach(function (t, ti) {
        order.forEach(function (name) {
          if (!first && parts[name].treatments[ti].result === "beyond_noise") first = [ti, name];
        });
      });
      if (!first && order.length) first = [0, order[0]];
      if (first) select(first[0], first[1]);
    }
  }

  // ---------------------------------------------------------------------
  // Plain run

  function badge(label) {
    if (!label || !label.text) return null;
    return el("span", { class: "badge " + (label.tone || ""), title: label.title || null, text: label.text });
  }

  function renderRun() {
    var m = DATA.model;
    app.appendChild(themeButton());
    app.appendChild(el("h1", { text: m.headline }));
    if (m.subhead) app.appendChild(el("p", { class: "muted", text: m.subhead }));
    app.appendChild(el("div", { class: "meta" }, (m.meta || []).map(function (x) {
      return el("span", null, [x.label + " ", el("b", { text: x.value })]);
    })));
    if (m.stopped) app.appendChild(el("p", { class: "card", text: "The run stopped early (" + m.stopped.replace(/_/g, " ") + ")." }));

    var table = el("section", { class: "card" }, [el("h2", { text: "Production against each model" })]);
    var head = el("tr", null, [el("th", { text: "" }), el("th", { class: "col", text: "Production" })].concat(m.columns.map(function (c) {
      var sub = [c.label !== c.model ? c.model : "", c.settings && c.settings.toLowerCase() !== "default settings" ? c.settings : ""].filter(Boolean).join(", ");
      return el("th", { class: "col" }, [c.label, sub ? el("div", { class: "faint", text: sub }) : null]);
    })));
    var rows = m.rows.map(function (r) {
      return el("tr", null, [
        el("td", null, [r.label, r.sublabel ? el("div", { class: "faint", text: r.sublabel }) : null]),
        el("td", null, [r.production, r.floor ? el("span", { class: "delta", text: "rerun gap " + r.floor }) : null]),
      ].concat(r.cells.map(function (c) {
        return el("td", { class: "cells", title: c.title || null }, [c.text, badge(c.label), c.delta ? el("span", { class: "delta", text: c.delta }) : null]);
      })));
    });
    table.appendChild(el("div", { class: "scroll" }, [el("table", null, [el("thead", null, [head]), el("tbody", null, rows)])]));
    app.appendChild(table);

    if (m.cost && m.cost.length) {
      app.appendChild(el("section", { class: "card" }, [el("h2", { text: "Cost" }), el("div", { class: "scroll" }, [el("table", null, [
        el("thead", null, [el("tr", null, [el("th", { text: "Arm" }), el("th", { class: "num", text: "Per call" }), el("th", { class: "num", text: "Change" }), el("th", { class: "num", text: "Monthly" })])]),
        el("tbody", null, m.cost.map(function (c) {
          return el("tr", null, [el("td", { text: c.label + (c.model ? " (" + c.model + ")" : "") }), el("td", { class: "num", text: c.text }),
            el("td", { class: "num", text: c.delta || "" }), el("td", { class: "num", text: c.monthlyText || "" })]);
        }))
      ])])]));
    }
    if (m.latency && m.latency.length) {
      app.appendChild(el("section", { class: "card" }, [el("h2", { text: "Latency" }), el("div", { class: "scroll" }, [el("table", null, [
        el("thead", null, [el("tr", null, [el("th", { text: "Arm" }), el("th", { class: "num", text: "p10" }), el("th", { class: "num", text: "Median" }), el("th", { class: "num", text: "p90" }), el("th", { class: "num", text: "Change" })])]),
        el("tbody", null, m.latency.map(function (l) {
          return el("tr", null, [el("td", { text: l.label }), el("td", { class: "num", text: ms(l.p10) }), el("td", { class: "num", text: ms(l.p50) }),
            el("td", { class: "num", text: ms(l.p90) }), el("td", { class: "num", text: l.delta || "" })]);
        }))
      ])])]));
    }
    if (m.floor && m.floor.text && m.floor.text.length) {
      app.appendChild(el("section", { class: "card" }, [el("h2", { text: "Production against itself" })].concat(m.floor.text.map(function (t) { return el("p", { text: t }); }))));
    }
    if (m.cases && m.cases.length) {
      var section = el("section", { class: "card" }, [el("h2", { text: "Cases" })]);
      m.cases.forEach(function (c) {
        var body = el("div", { class: "case-body" });
        var details = el("details", { class: "case" }, [el("summary", null, [
          el("span", { class: "status " + (c.differs ? "changed" : c.status === "replayed" ? "same" : "other"), text: c.differs ? "differs" : c.status === "replayed" ? "same" : "not replayed" }),
          el("span", { class: "preview", text: c.input || "" }),
          el("span", { class: "faint mono", text: c.traceId })
        ]), body]);
        var drawn = false;
        details.addEventListener("toggle", function () {
          if (!details.open || drawn) return;
          drawn = true;
          var request = (c.system ? "[system]\n" + c.system + "\n\n" : "") + (c.messages || []).map(function (x) { return "[" + x.role + "]\n" + x.content; }).join("\n\n");
          if (request) body.appendChild(el("div", { class: "request", text: request }));
          var arms = [c.production].concat(c.arms || []).concat(c.floor || []);
          body.appendChild(el("div", { class: "replies" }, arms.map(function (a) {
            var text = a.pretty || a.output || "";
            return el("div", { class: "reply" }, [
              el("h4", null, [a.label, el("span", { text: [a.latency, a.cost].filter(Boolean).join(" · ") })]),
              a.error ? el("div", { class: "problem", text: a.error }) : null,
              (a.changes || []).length ? el("div", { class: "problem" }, a.changes.map(function (ch) { return el("div", { text: ch.path + ": " + ch.from + " → " + ch.to }); })) : null,
              text ? el("pre", { text: text }) : el("pre", { class: "empty", text: "(" + a.status + ")" })
            ]);
          })));
        });
        section.appendChild(details);
      });
      app.appendChild(section);
    }
    app.appendChild(el("div", { class: "foot" }, [el("p", { text: DATA.footer })]));
  }

  if (DATA.kind === "map") renderMap(); else renderRun();
})();
