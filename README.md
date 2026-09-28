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
| `deploy/homelab-helm-charts/` | Files to copy into the GitOps repo (Application, values, Grafana SA) |
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

1. Panel driver on the node: see `docs/panel-driver.md` (make `udl` persistent via DKMS).
2. Image: `.github/workflows/image.yml` builds `linux/arm64,amd64` and pushes
   `dsmithson/rack-display` (needs `DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN` repo secrets).
3. Grafana token: add `deploy/homelab-helm-charts/grafana-terraform/rack-display.tf`
   to `grafana-terraform/`, apply, then
   `kubectl create namespace rack-display` and
   `kubectl create secret generic rack-display-grafana -n rack-display --from-literal=GRAFANA_TOKEN=$(terraform output -raw rack_display_token)`.
4. GitOps: copy `deploy/homelab-helm-charts/argoCD/applications/rack-display.yaml` and
   `deploy/homelab-helm-charts/rack-display/values.yaml` into homelab-helm-charts;
   add `rack-display` to the reflector namespace lists in
   `cert-manager/wildcard-int-knightware-net.yaml`. `rackdisplay.int.knightware.net`
   is already covered by the `*.int.knightware.net` wildcard DNS record.

The pod is privileged (DRM master on `/dev/dri`) and pinned to `turing01-04`.
Only one process can drive the panel: stop the pod before running `displaytest` there.
If the panel is absent, the app keeps serving the web view and retries every 30s.
