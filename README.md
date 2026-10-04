# homelab-rack-display

Infographic status screens for the homelab, shown on a 1440x240 USB
DisplayLink bar panel in the rack (on `turing01-04`) and served as a web page at
`rackdisplay.int.knightware.net`.

```
Prometheus ─┐                ┌─> GET /                 rotating display (browser, scales to fit)
            ├─> collector ──>│   GET /screens/<name>     one screen, live data injected (?live=15 to self-refresh)
Grafana  ───┘  (every 15s)   │   GET /api/data           the data document
 (alerts)                    │   GET /frame.png          last panel frame (embed as an image)
                             └─> headless Chromium ──> screenshot (1 fps) ──> DRM/KMS ──> udl ──> panel
```

The screens are plain HTML/CSS/SVG (`design/screens/`) bound to one JSON data
document by `design/display.js`. The panel is a headless Chromium screenshotting
the same rotation page a browser sees, so the web view and the panel always match.

## Layout

| Path | What |
|---|---|
| `cmd/rack-display` | The app: collector + HTTP server + panel renderer |
| `cmd/displaytest` | Hardware smoke test: test pattern, PNG slideshow, or mirror a remote `/frame.png` |
| `config/display.json` | **All** data bindings (PromQL), thresholds and rotation |
| `design/` | Screens, design tokens, runtime JS, fonts (embedded into the binary); see `design/README.md` |
| `internal/drm` | DRM/KMS dumb-buffer output (incl. the udl 640x480 minimum-framebuffer workaround) |
| `internal/collector` | Evaluates bindings into the data document |
| `chart/` | Helm chart (ArgoCD consumes it from this repo) |
| `deploy/udl-dkms/` | DKMS config for the panel's `udl` kernel module |
| `docs/` | Data-source survey, panel driver setup |

## Configuration (`config/display.json`)

Each key under `data` is a dotted path that screens reference with `data-bind`:

```jsonc
"wan.down_bps":     { "query": "sum(rate(...[2m])) * 8" },                  // instant -> number
"wan.down_mbps_6h": { "query": "...", "range": "6h", "step": "5m" },         // range -> array
"ups.state":        { "query": "upsBasicOutputStatus == 1",
                      "label": "upsBasicOutputStatus", "map": { "onLine": "On line" } },
"ping": { "table": {                                                          // vector -> list of objects
  "query": "avg_over_time(probe_duration_seconds[5m]) * 1000", "key": ["target_name"],
  "columns": { "target": { "label": "target_name" }, "rtt_ms": { "round": 1 },
               "loss_pct": { "query": "...", "default": 0 } },                // joined on key labels
  "sort_by": "target", "limit": 3 } },
"alerts": { "grafana_alerts": { "max_firing": 4 } }
```

Column sources: `const`, `query` (joined on `key`; with `label` reads a label of
the joined series; with `range` yields an array), `label`, `template`
(`"{a} · {b}"`, `else` used when a reference is empty), else the row's value.
Transforms: `hex`, `regex`/`replace`, `scale`, `round`, `map` (`"*"` = fallback).

`thresholds` override the screens' default warn/crit (list fields use
`list.field`, e.g. `nodes.temp_c`). `rotation` lists screens and dwell times;
`variants` swap a slot while a condition holds (`jellyfin.streams == 0`), and
`priority` interleaves a screen after every slot (critical alerts).

## Develop

```bash
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 19090:9090 &
go run ./cmd/rack-display -prometheus http://localhost:19090 -web design -listen :18080
# http://localhost:18080/          rotation      /screens/   all screens
# add -panel none -chrome <chromium> to also render /frame.png

# see it on the real panel from your machine:
make deploy   # copies displaytest to the node
ssh -R 18080:127.0.0.1:18080 192.168.86.144 '~/displaytest -url http://127.0.0.1:18080/frame.png -fps 1'
```

`design/render.sh` renders the screens with their built-in mock data (no
server needed) for design iteration.

## Deploy

Runs in the homelab cluster via ArgoCD. The cluster-side pieces live in
[homelab-helm-charts](https://github.com/dsmithson/homelab-helm-charts):
`argoCD/applications/rack-display.yaml` (chart from this repo's `chart/`),
`rack-display/values.yaml`, and the Grafana service account in
`grafana-terraform/rack-display.tf` (token in secret
`rack-display/rack-display-grafana`).

The pod is privileged (DRM master on `/dev/dri`) and pinned to `turing01-04`,
which needs the `udl` kernel module: see `docs/panel-driver.md`. Only one
process can drive the panel, so stop the pod before running `displaytest`
there. If the panel is absent, the app keeps serving the web view and retries
every 30s.

## Releases

`.github/workflows/image.yml` builds `linux/arm64` + `linux/amd64` images to
Docker Hub `dsmithson/rack-display`:

| Push | Image tags |
|---|---|
| `main` (code changes) | `latest`, `sha-<short>` |
| `main` (only `*.md`, `docs/`, `deploy/`, `design/renders/`) | none, build skipped |
| tag `v1.2.3` | `1.2.3`, `1.2`, `1` |

### Why it works this way

- **The cluster runs a pinned release, never `latest`.** ArgoCD self-heals
  and the old `latest` + `pullPolicy: Always` setup meant any pod restart
  (node reboot, eviction) could silently pick up whatever `main` last built.
  With a fixed tag, what runs only changes when homelab-helm-charts changes,
  so every deploy is a reviewable commit there and a rollback is reverting it.
- **The chart is pinned too.** ArgoCD reads `chart/` from this repo, so
  `targetRevision: main` would let a chart edit on `main` roll out on its own,
  possibly ahead of (or behind) the image it expects. Pinning the chart to the
  same `vX.Y.Z` tag as the image keeps the two in lockstep.
  The chart's own default `image.tag` is empty, meaning its `appVersion`, so
  even without the override a chart at `vX.Y.Z` runs image `X.Y.Z`.
- **`main` still publishes `latest` and `sha-<short>`** as a dev channel for
  trying a build before cutting a release (e.g. temporarily point the values
  at `sha-abc1234`).
- **Doc-only pushes skip the build.** They can't change the image, so a
  rebuild would only burn CI minutes and churn `latest` to a bit-identical
  image. Tag pushes always build (GitHub ignores path filters for tags), so
  tagging a docs-only commit still produces a release.
- **Semver, loosely:** patch for fixes and query/threshold tweaks, minor for
  new screens or bindings, major for breaking config/chart changes (e.g.
  renamed values or data keys). `X.Y` and `X` tags float to the newest
  release in that line if you'd rather track fixes automatically, but the
  cluster pins the exact version.

### Cutting a release

```bash
# 1. bump appVersion in chart/Chart.yaml to 1.2.3, commit and push to main
# 2. tag that commit
git tag -a v1.2.3 -m "v1.2.3: <summary>" && git push origin v1.2.3
# 3. wait for the image workflow (gh run watch), confirm dsmithson/rack-display:1.2.3
# 4. in homelab-helm-charts, bump both pins and push:
#      rack-display/values.yaml               image.tag: "1.2.3"
#      argoCD/applications/rack-display.yaml  targetRevision: v1.2.3
```

Rollback: revert step 4 in homelab-helm-charts; the older image and chart tag
are still published.
