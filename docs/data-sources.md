# Data sources for the rack display

Survey of `../homelab-helm-charts` (2026-09-28). Dashboards/alerts live in
`../homelab-helm-charts/grafana-terraform/`; deployment is ArgoCD app-of-apps
(`argoCD/applications/*.yaml`).

## Endpoints (in-cluster)

| What | URL | Auth |
|---|---|---|
| Prometheus | `http://kube-prometheus-stack-prometheus.monitoring.svc:9090` (verify svc name) | none |
| Grafana | `http://kube-prometheus-stack-grafana.monitoring.svc` | admin only today; needs a Viewer service-account token |
| Grafana firing alerts | `GET /api/prometheus/grafana/api/v1/alerts` | token |
| Loki | `http://loki.monitoring.svc:3100` | none |

Alert rules are Grafana-managed (Terraform), so firing alerts must come from the
Grafana API, not Alertmanager.

## Metrics worth displaying

### Internet / WAN (`internet-wan-health` dashboard)
- Live WAN bps: `rate(node_network_{receive,transmit}_bytes_total{instance="opnsense-01",device="ix0_vlan100"}[5m]) * 8`
  - `ix0_vlan100` will change when WAN moves to a dedicated 1G port.
- Speedtest (hourly): `speedtest_download_bits_per_second`, `speedtest_upload_bits_per_second`,
  `speedtest_ping_latency_milliseconds`, `speedtest_jitter_latency_milliseconds`
- ICMP probes (Cloudflare/Google/Quad9, `target_name` label, job `blackbox-icmp`):
  RTT `probe_duration_seconds * 1000`, loss `100 * (1 - avg_over_time(probe_success[5m]))`

### LAN (TP-Link TL-SG3428X, 192.168.86.53)
- Total: `sum(rate(ifHCInOctets{instance="192.168.86.53"}[5m])) * 8` (and `ifHCOutOctets`)
- Per-port bps, `ifOperStatus`, `ifSpeed`

### Cluster (9 nodes: 8x RK1 arm64 turing01-0[1-4], turing02-0[1-4]; ds-k8s-media-01 amd64)
- CPU %: `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
- Mem %: `(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100`
- SoC temp (unused by any dashboard today): `node_thermal_zone_temp{type=~"bigcore.*|littlecore.*|center.*|gpu.*|npu.*"}`
- Ready: `kube_node_status_condition{condition="Ready",status="true"}`
- CrashLoop: `kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff"} == 1`
- Degraded deployments: `(kube_deployment_spec_replicas - kube_deployment_status_replicas_available) > 0`

### Jellyfin (job `jellyfin-exporter`)
- Up `jellyfin_up`; streams `count(jellyfin_now_playing_state)`; `jellyfin_transcoding_sessions`;
  active users `count(jellyfin_user_active)`
- Now playing: `jellyfin_now_playing_state == 1` labels `username, device, title, series_title, series_season, series_episode`
- Transcode detail: `jellyfin_transcoding_session_info{video_codec,audio_codec,hardware_acceleration}`,
  `jellyfin_transcoding_session_bitrate_bits_per_second`
- Library: `jellyfin_media_count{type="Movie|Series|Episode|Album|Song"}`

### Power: APC Smart-UPS X 3000 (192.168.1.37)
- `upsAdvBatteryCapacity` %, `upsAdvOutputLoad` % (no watts; derive from load),
  `upsAdvBatteryRunTimeRemaining_seconds`, `upsBasicBatteryStatus` (1 = normal), input/output voltage

### Storage: Synology DS-HomeNAS (192.168.86.61)
- Volume used %: `(1 - raidFreeSize{raidName=~"Volume.*"} / raidTotalSize) * 100`
- `diskStatus`, `diskTemperature`, `raidStatus`; iSCSI read-only count
- Stale backups: `(time() - kube_cronjob_status_last_successful_time{cronjob=~".*backup.*"}) / 3600 > 26`

### DNS
- Technitium (last-hour totals, not counters): `technitium_queries_total`, block % from
  `technitium_queries_blocked`, cache % from `technitium_queries_cached`, `technitium_clients_unique`
- Unbound (OPNsense): `rate(unbound_total_num_queries[5m])`, cache hit %

### Alerts (Grafana, Terraform-managed)
~12 critical + ~25 warning rules (storage, UPS, node NotReady/fs/cpu/mem, WAN loss,
CrashLoop/OOM, Technitium, backups, OpenClaw, cert expiry). Contact points: Discord + OpenClaw webhook.

## Gaps
- No per-app HTTP health checks (blackbox is ICMP only); *arr apps, Immich, HA only via pod state.
- No read-only Grafana service account yet.
