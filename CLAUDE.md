# homelab-rack-display

Go app that renders homelab status screens (HTML/CSS/SVG in `design/screens/`)
for a 1440x240 USB DisplayLink panel in the rack, and serves the same screens
at `rackdisplay.int.knightware.net`. `README.md` has the full layout, config
format and release rationale; this file is the short version plus the
non-obvious bits.

## How it works

- `internal/collector` evaluates every binding in `config/display.json`
  (PromQL + Grafana alert state) every 15s into one JSON document.
- `internal/server` injects that document into each screen's
  `<script id="data">`; `design/display.js` binds it into the DOM.
  `design/index.html` rotates screens through two iframes using
  `/api/playlist`.
- With `-panel`, `internal/panel` runs headless Chromium (chromedp) on the
  rotation page, screenshots it every second and pushes changed frames to the
  panel via DRM/KMS (`internal/drm`). Web view and panel are the same page, so
  they always match.
- `design/` (screens, fonts, JS) is embedded into the binary; `-web design`
  serves it from disk for development.

## Develop and test

```bash
go vet ./... && go test ./...      # what CI runs
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 19090:9090 &
go run ./cmd/rack-display -prometheus http://localhost:19090 -web design -listen :18080
```

There is no Chromium on the WSL dev box (the snap stub doesn't work). To
exercise the panel renderer, build the image and run it with `-panel none`
(`/frame.png` shows the captured frame). From inside Docker, reach a
port-forward with `--address 0.0.0.0` and `http://host.docker.internal:19090`.

## Release and deploy

The cluster runs a **pinned** release via ArgoCD from
`../homelab-helm-charts` (never `latest`). Patch for fixes and query tweaks,
minor for new screens/bindings, major for breaking config or chart changes.

1. Bump `appVersion` (and chart `version`) in `chart/Chart.yaml`, commit, push
   `main`.
2. `git tag -a vX.Y.Z -m "vX.Y.Z: <summary>" && git push origin vX.Y.Z`
3. Wait for the `image` workflow on the tag (`gh run list --workflow image.yml`,
   then `gh run watch <id>`), then confirm
   `docker manifest inspect dsmithson/rack-display:X.Y.Z` lists arm64.
   **Don't skip this:** ArgoCD auto-syncs, so bumping the pins before the
   image exists leaves the pod in `ImagePullBackOff`.
4. In homelab-helm-charts bump **both** pins together and push `main`:
   `rack-display/values.yaml` `image.tag: "X.Y.Z"` and
   `argoCD/applications/rack-display.yaml` `targetRevision: vX.Y.Z`.
   Stage only those files; that repo often has unrelated untracked work.
5. Check the rollout: `kubectl -n rack-display get pods -o wide` and the logs.

Rollback = revert step 4.

## Gotchas

- **One pod only, privileged, pinned to `turing01-04`.** Only one process can
  be DRM master on the panel (`strategy: Recreate`), so stop the pod before
  running `cmd/displaytest` on the node. The panel (UV01DA) is DisplayLink
  DL-1x5 and needs the out-of-tree `udl` kernel module, not evdi; see
  `docs/panel-driver.md`.
- **Chromium memory.** The headless renderer grows ~150 MB/day (1 fps
  screenshots plus iframe rotation) and never gives it back. This OOM-killed
  the pod at its 1 GiB limit (fixed in v1.0.1). `-browser-recycle`
  (chart `panel.browserRecycle`, default 6h) restarts the browser to cap it.
  Don't disable it without a replacement. To debug memory, look at per-process
  `VmRSS` in the pod: the `--type=renderer` process without `top-chrome` is
  the page; the Go binary is ~20 MB.
- **tini is PID 1** in the image so Chromium helpers orphaned by each recycle
  get reaped. Keep it if you change the entrypoint.
- Chart `image.tag` defaults to `.Chart.AppVersion` (never `latest`), so
  `appVersion` must match the tag you cut.
- Chart `args` are appended to the image `ENTRYPOINT`; don't repeat
  `rack-display` in them.
- Doc-only pushes (`*.md`, `docs/`, `deploy/`, `design/renders/`) skip the image
  build on `main`; tag pushes always build.
