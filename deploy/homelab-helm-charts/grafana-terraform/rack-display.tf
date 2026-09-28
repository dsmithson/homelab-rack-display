# Read-only service account for the rack display (reads alert rule state via
# /api/prometheus/grafana/api/v1/rules).
resource "grafana_service_account" "rack_display" {
  name        = "rack-display"
  role        = "Viewer"
  is_disabled = false
}

resource "grafana_service_account_token" "rack_display" {
  name               = "rack-display"
  service_account_id = grafana_service_account.rack_display.id
}

output "rack_display_token" {
  value     = grafana_service_account_token.rack_display.key
  sensitive = true
}
