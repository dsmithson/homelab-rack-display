---
name: new-screen
description: Add or change a rack-display screen end to end - find the metrics, design the 1440x240 HTML screen, wire its bindings in config/display.json, and verify it against live Prometheus. Use when asked to "add a screen", make a "new rack display screen", "show X on the rack display", add a panel/tile/binding to an existing screen, or redesign one.
---

# Adding or changing a rack-display screen

`design/README.md` is the design system (tokens, frame and rail, palette and RGB565
rules, status shapes, the `data-bind` contract and chart types). Read it in full;
this skill is the workflow around it and the traps found the hard way.

## 1. Find out what data exists

- **Dashboards:** `../homelab-helm-charts/grafana-terraform/dashboards/*.json`. Pull
  every panel's queries at once:
  ```bash
  python3 -c "import json,sys
  def w(ps):
    for p in ps:
      print('--',p.get('type'),'|',p.get('title'))
      for t in p.get('targets',[]): print('   ',t.get('expr'))
      w(p.get('panels',[]))
  w(json.load(open(sys.argv[1]))['panels'])" ../homelab-helm-charts/grafana-terraform/dashboards/<name>.json
  ```
- **Service config:** `../homelab-helm-charts/<service>/values.yaml` explains labels
  and exporters (e.g. why a label was excluded). **Alert rules:**
  `grafana-terraform/alert_rules_*.tf`; mirror their thresholds.
- **Real values:** run `scripts/promq` against the port-forward from CLAUDE.md.
  `promq -m <substr>` lists metrics, `promq '<expr>'` gives instant values,
  `promq -r 7d -s 1d '<expr>'` shows series shape. Check magnitudes, label values,
  duplicates and gaps before designing.

## 2. Design

- Read `design/README.md`, `tokens.css`, `display.js`, two or three sibling
  `design/screens/*.html`, and look at `design/renders/*.png`.
- Write `design/screens/NN-name.html` with mock data in `<script id="data">`.
  **Ground the mock in real numbers** from step 1.
- `cd design && ./render.sh`, then **look at** `design/renders/NN-name.png`. Iterate
  until it reads from 1-3 m and matches its siblings.
- Render again with zero/empty mock data (no traffic, empty lists) and make sure it
  still looks deliberate ("–", empty tracks, an empty-state caption). Then restore
  the realistic mock.
- For bursty data, pick a window that actually shows something (a 24h window can be
  empty for days; 7d daily bars work), or make two screens.
- Prefer existing components and chart types. If you add one to `display.js`, keep
  it general and document it in the README's chart list.

## 3. Data traps

- **Sticky gauges.** Some state gauges only update on traffic. `litellm_deployment_state`
  held "outage" for days on unused deployments. Gate on recent activity:
  `... and on (x) (sum by (x) (increase(<requests>[1h])) > 0)`.
- **Duplicate series** across pods, scrape targets or model_ids: wrap in
  `max by (<identity label>)` before `count` or `sum`.
- **NaN or empty results:** give the binding `"default": null` (renders "–"), guard
  division with `/ (denominator > 0)`, and add `or vector(0)` for counts that should
  read 0.
- **Ranges:** Go durations have no `d` (use `144h`, not `6d`). `prom.Align` returns
  range/step + 1 points, so 144h at 24h gives 7 bars. Prometheus omits empty steps;
  promq says when it does.
- **Table joins:** a joined column's `default` is used literally (it does not go
  through `map`).

## 4. Wire it up

- Add bindings, thresholds and a `rotation` entry to `config/display.json`. The JSON
  config is the source of truth; HTML `data-warn`/`data-crit` are only defaults.
- The rail status stripe is computed by `display.js` from the screen's thresholded
  elements. Don't hand-set `body[data-status]` to anything but the mock's honest
  state.
- `go vet ./... && go test ./...` (add a collector test if you change collector code).
- Run against live data and look at the result:
  ```bash
  go run ./cmd/rack-display -prometheus http://localhost:19090 -web design -listen :18080 &
  curl -s localhost:18080/api/data | python3 -m json.tool | less   # check your keys, meta.errors
  CH=$(ls -d ~/.cache/ms-playwright/chromium_headless_shell-*/chrome-linux/headless_shell | tail -1)
  "$CH" --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
    --window-size=1440,240 --virtual-time-budget=3000 \
    --screenshot=/tmp/live.png http://localhost:18080/screens/NN-name.html
  ```
  "ALERTS N/A" in the rail is expected locally (no Grafana token). Stop the server
  when done.
- Update the screens table (and any new chart types or binding notes) in
  `design/README.md`, and re-run `render.sh` if shared markup changed.

## 5. Optional: designer then developer agents

Splitting the work keeps each agent focused:

- **Designer** gets the metrics brief (metric names, label meanings, real values,
  working PromQL, what's missing) plus the README, sibling screens and renders. It
  delivers the HTML mockup and a binding table: key -> PromQL -> range/step ->
  shape/format -> thresholds. It doesn't touch Go or config.
- **Developer** gets that table as the spec, plus any data traps you have already
  verified. It wires config, checks collector support, verifies each query live,
  screenshots the live page and updates the docs.

Check the trickier data claims yourself between the two steps (sticky state, point
counts).

## 6. Release

A new screen or binding is a **minor** release. Follow CLAUDE.md "Release and
deploy" exactly, including waiting for the arm64 image before bumping the pins.
