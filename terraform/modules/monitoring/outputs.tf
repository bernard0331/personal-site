output "dashboard_id" {
  value = google_monitoring_dashboard.site.id
}

output "slos" {
  description = "SLO resource names, by site and kind"
  value = merge(
    { for k, v in google_monitoring_slo.availability : "${k}/availability" => v.name },
    { for k, v in google_monitoring_slo.latency : "${k}/latency" => v.name },
  )
}
