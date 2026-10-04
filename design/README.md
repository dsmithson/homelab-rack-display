# Rack display: screen designs

Static HTML mockups for the 1440×240 USB bar display (DisplayLink DL-1x5, RGB565).
Each screen is one HTML file that Chromium renders headless and the daemon pushes to the panel.

```
design/
  tokens.css        shared design system (colours, type, spacing, tiles, meters, status glyphs)
  display.js        tiny runtime: binds JSON data into [data-bind] and draws inline-SVG charts
  fonts/            Roboto + Roboto Condensed (Apache-2.0), embedded so renders never depend on host fonts
  screens/NN-*.html one file per screen/state, each with its mock data in <script id="data">
  renders/*.png     1440×240 renders of every screen
  render.sh         re-renders everything
```

## Rendering

`./render.sh`. **Gotcha:** `chromium --headless=new --window-size=1440,240` gives a viewport only
about 153 px tall, because new-headless still reserves room for window chrome. The PNG is 1440×240, but
the bottom ~87 px is page background. Old-headless is exact, so the script uses
**`chrome-headless-shell`**. It picks up Playwright's copy in `~/.cache/ms-playwright` when that exists
(`npx playwright install chromium`); otherwise set `CHROME=/path/to/chrome-headless-shell`. Chromium 132+
removed `--headless=old` from the main binary, so production should ship the headless-shell build, or
use CDP `Emulation.setDeviceMetricsOverride` to force 1440×240. `chromium-browser` on this WSL box is only
a snap stub and is not installed.

`display.js` runs synchronously and all fonts are local, so the default screenshot-on-load captures the
finished frame. No network access is needed.

## Visual direction

"Instrument panel, not dashboard." A calm near-black field with flat mid-grey tiles and white condensed
numerals at 52–104 px. At 1–3 m you read one big number per tile, its unit a step quieter, and a small
uppercase label above it. Colour is rationed:

* **Series colours** (identity) appear only in charts, swatches and meters. Blue = download/inbound, orange =
  upload/outbound, aqua = direct play/cached, violet = transcode.
* **Status colours** (state) are reserved for health. They always come with a shape: ● ok, ▲ warn, ■ crit,
  ○ unknown. When something needs attention the whole tile or cell is tinted amber or red with a 2 px outline,
  so you can spot it across the room even if you can't read it.
* Text is always white or grey, never a series colour.

Everything is flat: no gradients, shadows or transparency. Every token colour is **RGB565-exact**
(R/B multiples of 8, G a multiple of 4), so the panel shows exactly what the PNG shows. Area charts use
a pre-blended solid fill with a 2.5 px line on top. There are no near-black tints below `#101418`,
because those would crush on the panel.

### Frame (identical on every screen)

The left rail is 176 px wide and holds:
1. the screen icon and name (answers "what am I looking at");
2. the clock and date (a rack display that shows the time earns its place);
3. the **global alert badge**, green `ALL CLEAR` or red/amber `■1 ▲3 ALERTS`, so a firing alert is visible
   on every screen, not only on the Alerts screen;
4. rotation dots, with the current screen shown as a long pill.

A 6 px **status stripe** on the rail's left edge shows the *worst state on this screen* (green/amber/red),
set with `body[data-status]`. It is separate from the alert badge. The stripe is local health computed from
thresholds (e.g. a node at 73 °C); the badge is Grafana alert state. They can legitimately differ, as in the
cluster mock: red stripe (a CrashLoopBackOff), "all clear" badge. `display.js` sets it after binding to the
worst `data-status` among the screen's elements (rail excluded); a screen with no thresholded elements keeps
its static `body[data-status]`.

## Tokens (tokens.css)

| Group | Tokens |
|---|---|
| Surfaces | `--bg #080C10` · `--rail #101418` · `--surface #181C20` (tiles) · `--surface-2 #202830` (tracks, cells) · `--line #283038` · `--line-2 #384450` |
| Ink | `--ink #FFF` · `--ink-2 #B0B8C0` (labels, units) · `--ink-3 #788490` (captions, axes) |
| Series | `--c-1 #3888E8` blue · `--c-2 #E06028` orange · `--c-3 #20A878` aqua · `--c-4 #8880E0` violet, plus `-fill` variants (flat, pre-blended on bg) |
| Status | `--ok #10A810` · `--warn #F8B018` · `--crit #E83838` · `--unknown #788490`; tints `--ok-bg/--warn-bg/--crit-bg`; `--crit-solid #C82828` for full-bleed blocks |
| Type | hero 104 · xl 72 · lg 52 · md 36 · sm 26 · body 20 · label 16 (uppercase, +0.09em). Numerals are Roboto Condensed Bold; text is Roboto |
| Space | 8 px grid (4/8/12/16/24/32); stage padding 16; tile gap 12; radius 10 / 6 |

The series palette was checked with the dataviz palette validator against `--surface` in dark mode. Every
set of colours that actually appears together passes the all-pairs CVD and normal-vision checks: {blue, orange},
{blue, aqua, orange}, {aqua, violet}. **Rule:** violet (transcode) must never share a chart with blue. Those two
fail CVD separation when placed side by side.

Components: `.tile` (with `[data-status=warn|crit]` tint), `.label`, `.num.{hero,xl,lg,md,sm}` with
`.unit`, `.meter` (`--v` 0–100, fill turns amber/red past its thresholds), `.st` status glyph, `.sw` series
swatch, `.pill`.

## Screens, rotation and dwell

| # | File | Shows | Dwell |
|---|---|---|---|
| 0 | `00-overview.html` | One tile per domain: WAN, cluster, media, UPS, NAS, DNS, LAN | 12 s |
| 1 | `01-internet.html` | Live down/up (hero), 6 h throughput with a speedtest reference line, ping RTT/loss ×3 | 15 s |
| 2 | `02-cluster.html` | 9/9 ready, CPU/MEM/°C heat table for all 9 nodes grouped by chassis, pod problems | 15 s |
| 3 | `03-media.html` / `03b-media-idle.html` | Stream count, direct vs transcode split, up to 3 now-playing cards, library counts; idle state | 12 s (idle: 8 s) |
| 4 | `04-power-storage.html` | UPS battery ring, runtime, load (W derived), NAS volume, disk temps, backup freshness | 12 s |
| 5 | `05-dns-network.html` | Technitium queries/blocked/clients with resolved/cached/blocked split, LAN in/out 1 h, ports up, busiest ports | 12 s |
| 6 | `06-alerts-clear.html` / `06b-alerts-firing.html` | Calm all-clear, or crit/warn counts plus up to 4 firing alerts | 10 s (firing: see below) |
| 7 | `07-llm.html` | LiteLLM over 7 days: total tokens with local/Claude split, tokens per day (stacked local + Claude), top 3 requested models, proxy health (upstreams, failures, in flight, local generation speed) | 12 s |
| 7b | `07b-llm-24h.html` | Same layout over the last 24 h: tokens per hour (24 stacked bars, right-hand axis instead of per-bar totals), `*_24h` keys | 12 s |

Suggested behaviour:
* **When any critical alert fires**, show `06b` every other slot (overview, alerts, internet, alerts, …) and
  hold it for 20 s.
* **When only warnings fire**, keep the normal rotation. The rail badge already shows them on every screen.
* When nothing is playing, show the idle media screen for less time, or skip it on alternate cycles.

## Data binding

Every screen's values come from one JSON object (`<script type="application/json" id="data">`). To go live,
the renderer replaces that block with real values under the same keys; nothing else in the page changes.

Markup contract (implemented in `display.js`):

* `data-bind="a.b"` sets the text from the value, formatted by `data-fmt`: `bps` (auto b/K/M/Gb/s), `mbps`,
  `pct`, `pct1`, `ms`, `int`, `compact` (48.2K), `dur` (s → "42 min" / "1h 12m"), `ago`, `age`, `temp`,
  `tb`, `none` (status only), or plain text by default.
* `data-warn=">85" data-crit=">95"` (or `<`) sets `data-status` on the element, or on
  `closest(data-status-on)`.
* `.meter[data-bind]` sets the fill width (`data-max` rescales).
* `data-list="key"` + `<template>` repeats a block per array item. Inside it, a key starting with `.` is
  relative to the item, and `data-attr-X=".k"` copies `k` into attribute `X`.
* `svg[data-chart]`: `area` (multiple series on one shared y-scale; `data-fills`, `data-grid`, `data-ref`
  reference line, `data-xlabels`), `spark`, `bars`, `ring`, `stackbars` (vertical columns with one stacked
  segment per series, bottom-up in `data-bind` order, on one shared y-scale; empty buckets draw a 2 px stub;
  `data-colors`, `data-max`, `data-axis-w`, `data-values="1"` for a compact total above each bar, and
  `data-xlabels` centred under the bars when there is one label per bar), `stack` (100 % bar; an empty
  track when every part is 0).

### Key → metric mapping

WAN interface is `ix0_vlan100` today. It will change when WAN moves to a dedicated port, so keep it in config.

| Key | Source / PromQL |
|---|---|
| `wan.down_bps` | `rate(node_network_receive_bytes_total{instance="opnsense-01",device="ix0_vlan100"}[5m]) * 8` |
| `wan.up_bps` | `rate(node_network_transmit_bytes_total{instance="opnsense-01",device="ix0_vlan100"}[5m]) * 8` |
| `wan.down_mbps_6h` / `wan.up_mbps_6h` / `wan.down_mbps_1h` | same expressions `/ 1e6`, range query 6 h (or 1 h) at step 5 m, 72 points |
| `wan.loss_pct_max` | `max(100 * (1 - avg_over_time(probe_success{job="blackbox-icmp"}[5m])))` |
| `speedtest.down_bps` / `.down_mbps` / `.up_bps` | `speedtest_download_bits_per_second`, `speedtest_upload_bits_per_second` (`/1e6` for mbps) |
| `speedtest.ping_ms`, `speedtest.jitter_ms` | `speedtest_ping_latency_milliseconds`, `speedtest_jitter_latency_milliseconds` (bound in mock data, not shown yet) |
| `speedtest.age_s` | `time() - timestamp(speedtest_download_bits_per_second)` |
| `ping[].target` | `target_name` label |
| `ping[].rtt_ms` | `probe_duration_seconds{job="blackbox-icmp"} * 1000` by `target_name` |
| `ping[].loss_pct` | `100 * (1 - avg_over_time(probe_success{job="blackbox-icmp"}[5m]))` by `target_name` |
| `cluster.nodes_ready` / `nodes_total` / `nodes_notready` | `sum(kube_node_status_condition{condition="Ready",status="true"})` / `count(kube_node_info)` / difference |
| `cluster.pods_running` | `sum(kube_pod_status_phase{phase="Running"})` |
| `cluster.pod_problems` | count of CrashLoop + degraded below |
| `cluster.problems[]` | `kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff"} == 1` (sev 2, name=`container`, detail=`namespace`) ∪ `(kube_deployment_spec_replicas - kube_deployment_status_replicas_available) > 0` (sev 1, name=`deployment`, detail="Degraded a/b") |
| `cluster.cpu_avg_pct` | `100 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100` |
| `nodes[].cpu_pct` | `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)` |
| `nodes[].mem_pct` | `(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100` |
| `nodes[].temp_c` | RK1: `max by (instance) (node_thermal_zone_temp{type=~"bigcore.*\|littlecore.*\|center.*\|gpu.*\|npu.*"})`; media-01 (amd64): probably `node_hwmon_temp_celsius{chip=~".*coretemp.*"}` (to verify) |
| `nodes[].ready` | `kube_node_status_condition{condition="Ready",status="true"}` by `node` |
| `nodes[].short`, `.node` | static config (order and grouping: turing01-0[1-4], turing02-0[1-4], ds-k8s-media-01) |
| `jellyfin.up` | `jellyfin_up` |
| `jellyfin.streams` | `count(jellyfin_now_playing_state == 1)` |
| `jellyfin.transcodes` / `.direct` | `jellyfin_transcoding_sessions` / streams − transcodes |
| `jellyfin.users_active` | `count(jellyfin_user_active)` (in mock data, not displayed) |
| `jellyfin.sessions[]` | `jellyfin_now_playing_state == 1` labels: `.title` = `series_title` or `title`; `.subtitle` = "S{series_season} · E{series_episode} — {title}" or "Movie"; `.user` = `username`; `.device` = `device` |
| `jellyfin.sessions[].mode/.hw/.codec` | join to `jellyfin_transcoding_session_info{video_codec,audio_codec,hardware_acceleration}`; `mode="transcode"` if matched |
| `jellyfin.sessions[].bitrate_bps` | `jellyfin_transcoding_session_bitrate_bits_per_second` |
| `jellyfin.library.{movie,series,episode,album}` | `jellyfin_media_count{type="Movie\|Series\|Episode\|Album"}` |
| `ups.battery_pct` | `upsAdvBatteryCapacity` |
| `ups.load_pct` | `upsAdvOutputLoad` |
| `ups.load_w` | derived: `upsAdvOutputLoad / 100 * 2700` (SMX3000 rated 2700 W; no watts OID) |
| `ups.runtime_s` | `upsAdvBatteryRunTimeRemaining_seconds` |
| `ups.on_battery` / `ups.state` | `upsBasicBatteryStatus != 1` → 1, "On battery"; else 0, "On line" (better: `upsBasicOutputStatus` if scraped) |
| `nas.volume_used_pct` | `(1 - raidFreeSize{raidName=~"Volume 1"} / raidTotalSize) * 100` |
| `nas.volume_free_tb` / `volume_total_tb` | `raidFreeSize / 1e12`, `raidTotalSize / 1e12` |
| `nas.raid_status` / `raid_text` | `raidStatus` (1 = Normal) → text |
| `nas.disks[]` | `diskTemperature` / `diskStatus` by `diskID` → `.name` D1…, `.temp_c`, `.status` (ok/warn/crit from diskStatus + temp > 50) |
| `backups[]` | `time() - kube_cronjob_status_last_successful_time{cronjob=~".*backup.*"}` → `.age_s`, `.name` = `cronjob` (sort by age desc, top 4); warn > 26 h, crit > 48 h |
| `dns.queries` | `sum(technitium_queries_total)` (last-hour total) |
| `dns.blocked` / `cached` / `resolved` | `technitium_queries_blocked`, `technitium_queries_cached`, total − blocked − cached |
| `dns.*_pct` | each ÷ `dns.queries` × 100 |
| `dns.clients` | `technitium_clients_unique` |
| `dns.instances_up` | `count(up{job=~".*technitium.*"} == 1)` (job name to verify) |
| `dns.queries_24h` | `sum(technitium_queries_total)` range 24 h, step 1 h |
| `lan.in_bps` / `out_bps` | `sum(rate(ifHCInOctets{instance="192.168.86.53"}[5m])) * 8` / `ifHCOutOctets` |
| `lan.in_mbps_1h` / `out_mbps_1h` | same `/1e6`, range 1 h, step 1 m |
| `lan.ports_up` / `ports_total` | `count(ifOperStatus{instance="192.168.86.53"} == 1)` / physical port count (28) |
| `lan.top_ports[]` | `topk(3, rate(ifHCInOctets[5m])*8 + rate(ifHCOutOctets[5m])*8)` by `ifIndex`; `.name` = `ifAlias` · port; `.util_pct` = bps / (`ifHighSpeed`×1e6) × 100 |
| `alerts.critical` / `warning` | Grafana `GET /api/prometheus/grafana/api/v1/alerts`, count `state="firing"` by `labels.severity` |
| `alerts.firing[]` | same response: `.name` = `labels.alertname`, `.summary` = `annotations.summary`, `.severity`, `.for_s` = now − `activeAt`; sort crit first, then oldest |
| `alerts.rules_total` | Grafana ruler API (`/api/ruler/grafana/api/v1/rules`) rule count |
| `clock.time` / `clock.date` | the renderer's local time (leave out to use the browser clock) |

## Data we'd want but don't have

* **Jellyfin playback progress and poster art.** A thin progress bar and a small poster would make the cards
  much richer. Neither is in the exporter; the Jellyfin `/Sessions` API has both (`PositionTicks`, item image).
* **Media node temperature** (ds-k8s-media-01 is amd64, so `node_thermal_zone_temp` probably doesn't apply).
  The hwmon series needs checking.
* **UPS watts and input state.** Only load % exists. `upsAdvInputLineVoltage` / `upsBasicOutputStatus`
  would give "on line / on battery / bypass" directly.
* **Last alert resolved / time since last incident** for the all-clear screen. Needs Grafana state history.
* **Per-app HTTP health** (*arr apps, Immich, Home Assistant). Blackbox is ICMP only.
* **Port aliases on the switch** (`ifAlias`) so the busiest-ports list shows device names, not port numbers.
* **Unbound (OPNsense) q/s and cache hit** are available but not shown. They would fit on the DNS screen
  if Technitium is ever replaced or supplemented.
* A **read-only Grafana service-account token** for the alerts API.

## Notes for the daemon

* Threshold values live in the HTML attributes (`data-warn`/`data-crit`). They mirror the Grafana rules
  (e.g. NAS > 90 % warn, node mem > 90 %) but could move to the JSON config if you'd rather tune them in one place.
* Fixed SVG widths assume this layout. If a tile's width changes, update that `<svg width>` too.
* Motion (later): at 5 fps, the only motion worth adding is a slow rotation-progress fill on the active
  dot, and perhaps the chart's live dot pulsing. Avoid anything larger, because it will judder over DisplayLink.

---

## Live wiring (added when the app was built)

- The Go server (`cmd/rack-display`) replaces each screen's
  `<script id="data">` with the live data document when serving
  `/screens/<name>.html`; opening a screen as a file still uses its mock data.
- The binding → PromQL mapping now lives in **`config/display.json`**, which is
  the source of truth (the table above documents the original intent).
- **Thresholds** are configured in `config/display.json` under `thresholds`
  (e.g. `"nodes.temp_c": {"warn": ">70", "crit": ">80"}`); the `data-warn` /
  `data-crit` attributes in the HTML remain as defaults.
- New runtime features in `display.js`: `?live=N` self-refresh,
  `data-if` / `data-unless`, `data-max="auto"` area charts, rotation dots from
  the server's playlist, a ticking clock, and "–" for missing values.
- `index.html` is the rotator (double-buffered iframes); the panel renderer
  screenshots `/?panel`.
- **LLM screen (`07-llm`)**: daily bars are a range query of
  `increase(...[1d])` over `144h` at step `24h`, which gives exactly 7 points
  (rolling 24 h windows ending now, so they add up to the 7-day total; "today"
  is the last 24 h). Config durations are Go durations, so write `24h`, not `1d`.
- **Upstream health is gated on recent traffic.** `litellm_deployment_state`
  (0 ok, 1 partial, 2 outage) only changes when a request passes through a
  deployment, so idle deployments keep their last state for days (e.g. an alias
  stuck at 2 after an old 429 burst). `llm.upstream_state_max` and the "not ok"
  part of `llm.upstreams_ok` only count deployments with requests in the last
  hour (`increase(litellm_deployment_total_requests_total[1h]) > 0`, joined on
  `litellm_model_name`); `llm.upstreams_total` counts every deployment name.
  A deployment that fails and then gets no traffic (e.g. cooled down with no
  retries) drops out of the check an hour later.
