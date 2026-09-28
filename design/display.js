/* Rack display runtime: binds a JSON data object into the page and draws
   inline-SVG charts. No dependencies, no network. Synchronous, so a headless
   screenshot taken on load sees the finished frame.

   Data comes from <script type="application/json" id="data">. The Go server
   replaces its contents with live values when serving a screen; opened as a
   plain file, the mock data in each screen is used.

   Live mode: append ?live=N to a screen URL to re-fetch /api/data every N
   seconds and re-render in place (for embedding a single screen elsewhere).

   data-if="key" / data-unless="key" show an element only when the value is
   truthy / falsy (e.g. an "alert data unavailable" message).

   Thresholds: DATA.__thresholds["nodes.cpu_pct"] = {warn:">85", crit:">95"}
   overrides data-warn/data-crit for that key (list items use listKey.field).

   Markup contract
   ---------------
   data-bind="a.b.c"      value lookup (dot path; array index ok: a.0.b).
                          Inside a data-list template, a leading "." is
                          relative to the current item.
   data-fmt="..."         bps | mbps | pct | pct1 | ms | int | compact | dur |
                          ago | temp | tb | text (default text)
   data-nounit            print the number only
   data-warn / data-crit  thresholds; ">80" or "<25". Sets data-status=
                          ok|warn|crit on the element (or on data-status-on=
                          "closest-selector").
   .meter[data-bind]      sets --v (0-100) from the value (data-max to scale)
   data-list="key"        repeats the child <template> for each array item
   svg[data-chart]        area | spark | ring | bars | stack  (see below)
*/
(function () {
  const $data = document.getElementById("data");
  let DATA = $data ? JSON.parse($data.textContent) : {};
  const PRISTINE = document.body.cloneNode(true);
  const thr = (key) => (DATA.__thresholds || {})[key] || {};

  const get = (root, path) =>
    path.split(".").filter(Boolean).reduce((o, k) => (o == null ? o : o[k]), root);
  const lookup = (el, key, item) =>
    key.startsWith(".") ? get(item, key.slice(1)) : get(DATA, key);

  const sig3 = (v) => (v >= 100 ? v.toFixed(0) : v.toFixed(1));
  const commas = (v) => Math.round(v).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");

  // each formatter returns [number-text, unit-text]
  const FMT = {
    text: (v) => [String(v), ""],
    bps: (v) =>
      v >= 1e9 ? [(v / 1e9).toFixed(2), "Gb/s"] :
      v >= 1e6 ? [sig3(v / 1e6), "Mb/s"] :
      v >= 1e3 ? [sig3(v / 1e3), "Kb/s"] : [String(Math.round(v)), "b/s"],
    mbps: (v) => [sig3(v), "Mb/s"],
    pct: (v) => [Math.round(v).toString(), "%"],
    pct1: (v) => [v.toFixed(1), "%"],
    ms: (v) => [v < 100 ? v.toFixed(1) : Math.round(v).toString(), "ms"],
    int: (v) => [commas(v), ""],
    compact: (v) =>
      v >= 1e6 ? [sig3(v / 1e6), "M"] : v >= 1e3 ? [sig3(v / 1e3), "K"] : [String(Math.round(v)), ""],
    dur: (s) =>
      s >= 3600 ? [`${Math.floor(s / 3600)}h ${Math.round((s % 3600) / 60)}`, "m"] :
      [String(Math.round(s / 60)), "min"],
    ago: (s) =>
      s < 3600 ? [String(Math.round(s / 60)), "min ago"] :
      s < 172800 ? [String(Math.round(s / 3600)), "h ago"] : [String(Math.round(s / 86400)), "d ago"],
    age: (s) =>
      s < 3600 ? [String(Math.round(s / 60)), "m"] :
      s < 172800 ? [String(Math.round(s / 3600)), "h"] : [String(Math.round(s / 86400)), "d"],
    temp: (v) => [Math.round(v).toString(), "°"],
    tb: (v) => [v.toFixed(1), "TB"],
  };

  function thresh(spec, v) {
    if (spec == null || spec === "") return false;
    const m = /^([<>])\s*(-?[\d.]+)$/.exec(spec.trim());
    if (!m) return false;
    return m[1] === ">" ? v > +m[2] : v < +m[2];
  }

  function bindEl(el, item, listKey) {
    const key = el.getAttribute("data-bind");
    const v = lookup(el, key, item);
    if (v === undefined) return;
    if (v === null) {
      if (!el.classList.contains("meter") && el.tagName !== "svg" && el.getAttribute("data-fmt") !== "none") el.textContent = "–";
      return;
    }
    const full = key.startsWith(".") ? listKey + key : key;
    const T = thr(full);
    const warn = T.warn ?? el.getAttribute("data-warn"), crit = T.crit ?? el.getAttribute("data-crit");
    if (el.classList.contains("meter")) {
      const max = +(el.getAttribute("data-max") || 100);
      el.style.setProperty("--v", Math.max(0, Math.min(100, (v / max) * 100)));
    } else if (el.tagName !== "svg" && el.getAttribute("data-fmt") !== "none") {
      const [n, u] = (FMT[el.getAttribute("data-fmt") || "text"] || FMT.text)(v);
      if (el.hasAttribute("data-nounit") || !u) el.textContent = n;
      else { el.textContent = n; const s = document.createElement("span"); s.className = u === "°" ? "unit sym deg" : /^[%KM]$/.test(u) ? "unit sym" : "unit"; s.textContent = u; el.appendChild(s); }
    }
    if (warn != null || crit != null) {
      const st = thresh(crit, v) ? "crit" : thresh(warn, v) ? "warn" : "ok";
      const sel = el.getAttribute("data-status-on");
      (sel ? el.closest(sel) : el).setAttribute("data-status", st);
    }
  }

  function bindTree(root, item, listKey) {
    root.querySelectorAll("[data-list]").forEach((list) => {
      if (item === undefined && list.closest("template")) return;
      const tpl = list.querySelector(":scope > template");
      const lk = list.getAttribute("data-list");
      const arr = lookup(list, lk, item) || [];
      const max = +(list.getAttribute("data-max-items") || 99);
      arr.slice(0, max).forEach((it) => {
        const frag = tpl.content.cloneNode(true);
        const wrap = document.createElement("div");
        wrap.appendChild(frag);
        bindTree(wrap, it, lk.startsWith(".") ? listKey + lk : lk);
        [...wrap.children].forEach((c) => {
          // item-level attributes: data-attr-status=".severity" etc.
          [...c.attributes].forEach((a) => {
            if (a.name.startsWith("data-attr-")) c.setAttribute(a.name.slice(10), lookup(c, a.value, it));
          });
          list.appendChild(c);
        });
      });
    });
    root.querySelectorAll("[data-bind]").forEach((el) => {
      if (el.closest("template")) return;
      const key = el.getAttribute("data-bind");
      if ((item === undefined) === key.startsWith(".")) return;
      bindEl(el, item, listKey);
    });
    root.querySelectorAll("svg[data-chart]").forEach((svg) => {
      if (svg.closest("template")) return;
      const k = svg.getAttribute("data-bind") || "";
      if ((item === undefined) === k.startsWith(".")) return;
      drawChart(svg, item);
    });
  }

  // ---------------------------------------------------------------- charts
  const NS = "http://www.w3.org/2000/svg";
  const mk = (tag, attrs, parent) => {
    const e = document.createElementNS(NS, tag);
    for (const k in attrs) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  };
  const niceMax = (v) => {
    const p = Math.pow(10, Math.floor(Math.log10(v || 1)));
    for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (v <= m * p) return m * p;
    return 10 * p;
  };
  const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  const color = (c) => (c && c.startsWith("--") ? css(c) : c);

  function drawChart(svg, item) {
    const type = svg.getAttribute("data-chart");
    const keys = (svg.getAttribute("data-bind") || "").split(",").map((s) => s.trim());
    const series = keys.map((k) => lookup(svg, k, item));
    if (series.some((s) => s == null || (Array.isArray(s) && s.length === 0))) return;
    const T = thr(keys[0]);
    const W = +svg.getAttribute("width"), H = +svg.getAttribute("height");
    svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
    const opt = (n, d) => (T[n] != null ? T[n] : svg.hasAttribute("data-" + n) ? svg.getAttribute("data-" + n) : d);
    ({ area, spark, ring, bars, stack })[type](svg, series, W, H, opt);
  }

  /* area: one or more series on ONE shared y-scale. Flat fills (no gradient).
     data-colors="--c-1,--c-2"  data-max  data-grid="250,500,750"  data-unit
     data-xlabels="-6h,-4h,-2h,now"  data-axis-w (px reserved right for labels) */
  function area(svg, series, W, H, opt) {
    const colors = opt("colors", "--c-1").split(",");
    const unit = opt("unit", "");
    const axisW = +opt("axis-w", 0), xlabH = opt("xlabels", "") ? 20 : 0;
    const pw = W - axisW, ph = H - xlabH;
    // data-max="auto" scales to the data (grid at quarters); a fixed max
    // keeps a capacity view. Reference lines above the scale are omitted.
    const auto = opt("max", "") === "auto";
    const max = (!auto && +opt("max", 0)) || niceMax(Math.max(1, ...series.flat()));
    const y = (v) => ph - (v / max) * (ph - 4);
    const q = (f) => +(max * f).toPrecision(2);
    const grid = auto ? [q(0.25), q(0.5), q(0.75), max] : opt("grid", "").split(",").filter(Boolean).map(Number);
    grid.forEach((g) => {
      mk("line", { x1: 0, x2: pw, y1: y(g), y2: y(g), stroke: css("--line"), "stroke-width": 1 }, svg);
      if (axisW) mk("text", { x: pw + 8, y: y(g) + 5 }, svg).textContent = g + (unit ? " " + unit : "");
    });
    const fills = opt("fills", "").split(",");
    series.forEach((s, i) => {
      if (fills[i] === "0") return;
      const x = (j) => (j / (s.length - 1)) * pw;
      const pts = s.map((v, j) => `${x(j).toFixed(1)},${y(v).toFixed(1)}`);
      mk("path", { d: `M0,${ph} L${pts.join(" L")} L${pw},${ph} Z`, fill: color(colors[i] + "-fill") }, svg);
    });
    series.forEach((s, i) => {
      const x = (j) => (j / (s.length - 1)) * pw;
      const pts = s.map((v, j) => `${x(j).toFixed(1)},${y(v).toFixed(1)}`);
      mk("path", { d: `M${pts.join(" L")}`, fill: "none", stroke: color(colors[i]), "stroke-width": 2.5, "stroke-linejoin": "round" }, svg);
      if (opt("dot", "1") === "1") {
        mk("circle", { cx: pw, cy: y(s[s.length - 1]), r: 5, fill: color(colors[i]), stroke: css("--bg"), "stroke-width": 2 }, svg);
      }
    });
    mk("line", { x1: 0, x2: pw, y1: ph, y2: ph, stroke: css("--line-2"), "stroke-width": 2 }, svg);
    // reference line (e.g. last speedtest = measured capacity)
    const refKey = opt("ref", "");
    if (refKey) {
      const rv = get(DATA, refKey);
      if (rv != null && rv <= max) {
        mk("line", { x1: 0, x2: pw, y1: y(rv), y2: y(rv), stroke: css("--ink-2"), "stroke-width": 1.5, "stroke-dasharray": "6 6" }, svg);
        const t = mk("text", { x: 4, y: y(rv) - 7, class: "v" }, svg);
        t.textContent = opt("ref-label", "{v}").replace("{v}", Math.round(rv));
      }
    }
    const xl = opt("xlabels", "").split(",").filter(Boolean);
    xl.forEach((t, i) => {
      const tx = (i / (xl.length - 1)) * pw;
      mk("text", { x: tx, y: H - 2, "text-anchor": i === 0 ? "start" : i === xl.length - 1 ? "end" : "middle" }, svg).textContent = t;
    });
    // peak marker (selective direct label)
    if (opt("peak", "") !== "") {
      const s = series[+opt("peak")]; const pk = Math.max(...s); const j = s.indexOf(pk);
      const px = (j / (s.length - 1)) * pw;
      mk("line", { x1: px, x2: px, y1: y(pk), y2: y(pk) - 10, stroke: css("--ink-3"), "stroke-width": 1 }, svg);
      const t = mk("text", { x: px, y: y(pk) - 14, "text-anchor": px < 60 ? "start" : px > pw - 60 ? "end" : "middle", class: "v" }, svg);
      t.textContent = `peak ${Math.round(pk)}${unit ? " " + unit : ""}`;
    }
  }

  /* spark: single line, no axes; last point dotted. data-color, data-max, data-min */
  function spark(svg, series, W, H, opt) {
    const s = series[0]; const c = color(opt("color", "--c-1"));
    const lo = +opt("min", Math.min(...s)), hi = +opt("max", Math.max(...s)) || 1;
    const y = (v) => H - 3 - ((v - lo) / (hi - lo || 1)) * (H - 6);
    const x = (j) => (j / (s.length - 1)) * (W - 6);
    if (opt("fill", "0") === "1")
      mk("path", { d: `M0,${H} L${s.map((v, j) => `${x(j)},${y(v)}`).join(" L")} L${x(s.length - 1)},${H} Z`, fill: color(opt("color", "--c-1") + "-fill") }, svg);
    mk("path", { d: "M" + s.map((v, j) => `${x(j).toFixed(1)},${y(v).toFixed(1)}`).join(" L"), fill: "none", stroke: c, "stroke-width": 2, "stroke-linejoin": "round" }, svg);
    mk("circle", { cx: x(s.length - 1), cy: y(s[s.length - 1]), r: 3.5, fill: c }, svg);
  }

  /* ring: value 0..max as an arc. data-color, data-max, data-thick, data-warn/crit */
  function ring(svg, series, W, H, opt) {
    const v = series[0], max = +opt("max", 100), th = +opt("thick", 14);
    const r = Math.min(W, H) / 2 - th / 2, cx = W / 2, cy = H / 2;
    let c = color(opt("color", "--c-1"));
    if (thresh(opt("crit", ""), v)) c = css("--crit"); else if (thresh(opt("warn", ""), v)) c = css("--warn");
    mk("circle", { cx, cy, r, fill: "none", stroke: css("--surface-2"), "stroke-width": th }, svg);
    const f = Math.max(0, Math.min(1, v / max));
    const C = 2 * Math.PI * r;
    mk("circle", { cx, cy, r, fill: "none", stroke: c, "stroke-width": th, "stroke-dasharray": `${f * C} ${C}`,
      transform: `rotate(-90 ${cx} ${cy})`, "stroke-linecap": f > 0.98 ? "butt" : "round" }, svg);
  }

  /* bars: vertical columns, baseline anchored, 2px gaps. data-color, data-max */
  function bars(svg, series, W, H, opt) {
    const s = series[0]; const c = color(opt("color", "--c-1"));
    const max = +opt("max", 0) || niceMax(Math.max(...s)); const gap = 2;
    const bw = (W - gap * (s.length - 1)) / s.length;
    s.forEach((v, i) => {
      const h = Math.max(2, (v / max) * H);
      mk("rect", { x: i * (bw + gap), y: H - h, width: bw, height: h, rx: Math.min(2, bw / 2), fill: c }, svg);
    });
  }

  /* stack: 100% horizontal stacked bar from parts; data-colors; 2px surface gaps */
  function stack(svg, series, W, H, opt) {
    const colors = opt("colors", "--c-1,--c-2,--c-3").split(",");
    const tot = series.reduce((a, b) => a + b, 0) || 1; let x = 0; const gap = 3;
    series.forEach((v, i) => {
      const w = (v / tot) * (W - gap * (series.length - 1));
      mk("rect", { x, y: 0, width: Math.max(0, w), height: H, rx: 4, fill: color(colors[i]) }, svg);
      x += w + gap;
    });
  }

  // ---------------------------------------------------------------- clock
  function clock() {
    const c = DATA.clock || {};
    const d = new Date();
    const t = c.time || d.toTimeString().slice(0, 5);
    const ds = c.date || `${d.toLocaleDateString("en-US", { weekday: "short" })} ${d.getDate()} ${d.toLocaleDateString("en-US", { month: "short" })}`;
    document.querySelectorAll("[data-clock]").forEach((e) => (e.textContent = t));
    document.querySelectorAll("[data-date]").forEach((e) => (e.textContent = ds));
  }

  // rotation dots: <div class="dots" data-dots="7" data-on="2">
  function dots() {
    const r = DATA.rotation;
    document.querySelectorAll("[data-dots]").forEach((d) => {
      const n = r ? r.count : +d.getAttribute("data-dots"), on = r ? r.index : +d.getAttribute("data-on");
      for (let i = 0; i < n; i++) { const e = document.createElement("i"); if (i === on) e.className = "on"; d.appendChild(e); }
    });
  }

  // global alert badge in the rail (same on every screen)
  function badge() {
    const b = document.querySelector("[data-badge]"); if (!b) return;
    const a = DATA.alerts || { critical: 0, warning: 0 };
    if (a.unavailable) {
      b.classList.add("unknown");
      b.innerHTML = `<i class="st"></i><span style="letter-spacing:.06em">ALERTS N/A</span>`;
    } else if (a.critical > 0 || a.warning > 0) {
      b.classList.add(a.critical > 0 ? "firing" : "warning");
      b.innerHTML = (a.critical ? `<span class="n"><i class="st crit" style="background:#fff"></i>${a.critical}</span>` : "") +
        (a.warning ? `<span class="n"><i class="st warn"></i>${a.warning}</span>` : "") +
        `<span style="font-weight:500;font-size:15px;letter-spacing:.06em">ALERTS</span>`;
    } else {
      b.innerHTML = `<i class="st ok"></i><span style="letter-spacing:.06em">ALL CLEAR</span>`;
    }
  }

  function conditions() {
    document.querySelectorAll("[data-if]").forEach((e) => { if (!get(DATA, e.getAttribute("data-if"))) e.remove(); });
    document.querySelectorAll("[data-unless]").forEach((e) => { if (get(DATA, e.getAttribute("data-unless"))) e.remove(); });
  }

  function render() { conditions(); dots(); clock(); badge(); bindTree(document); }
  render();
  setInterval(clock, 1000);

  const live = +new URLSearchParams(location.search).get("live");
  if (live > 0) {
    setInterval(async () => {
      try {
        const r = await fetch("/api/data" + location.search, { cache: "no-store" });
        if (!r.ok) return;
        DATA = await r.json();
        document.body.replaceWith(PRISTINE.cloneNode(true));
        render();
      } catch (e) { /* keep last frame */ }
    }, live * 1000);
  }
})();
