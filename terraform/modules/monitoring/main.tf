# Alerts and a dashboard for a site behind the Gateway, in Cloud Monitoring.
#
# All three alerts are written in PromQL against the metrics Google Cloud
# already collects (nothing to install):
#   load balancer   loadbalancing.googleapis.com/https/request_count and
#                   total_latencies, measured at the Gateway's load balancer,
#                   so they count what visitors saw, including a pod that's
#                   down (the load balancer then answers 502/503 itself)
#   containers      kubernetes.io/container/restart_count from GKE
#
# Each alert needs a minimum of traffic before it fires, so one failed
# request on a quiet night isn't a 100% error rate.

locals {
  lb_requests = "loadbalancing_googleapis_com:https_request_count{monitored_resource=\"https_lb_rule\"}"
  lb_5xx      = "loadbalancing_googleapis_com:https_request_count{monitored_resource=\"https_lb_rule\",response_code_class=\"500\"}"
  lb_latency  = "loadbalancing_googleapis_com:https_total_latencies_bucket{monitored_resource=\"https_lb_rule\"}"
  pod_filter  = "monitored_resource=\"k8s_container\",cluster_name=\"${var.cluster}\",namespace_name=\"${var.namespace}\""
}

resource "google_monitoring_notification_channel" "email" {
  count        = var.notification_email == null ? 0 : 1
  display_name = "Site alerts (email)"
  type         = "email"
  labels = {
    email_address = var.notification_email
  }
}

locals {
  channels = google_monitoring_notification_channel.email[*].id
}

resource "google_monitoring_alert_policy" "errors" {
  display_name = "${var.namespace}: 5xx error rate"
  combiner     = "OR"
  severity     = "ERROR"
  conditions {
    display_name = "More than ${var.error_ratio * 100}% of requests failed (10 min, at least 20 requests)"
    condition_prometheus_query_language {
      query               = <<-EOT
        (sum(rate(${local.lb_5xx}[10m])) / sum(rate(${local.lb_requests}[10m])) > ${var.error_ratio})
        and on() (sum(increase(${local.lb_requests}[10m])) >= 20)
      EOT
      duration            = "300s"
      evaluation_interval = "60s"
    }
  }
  notification_channels = local.channels
  documentation {
    mime_type = "text/markdown"
    content   = <<-EOT
      The site's load balancer is returning 5xx responses. 502/503 from the
      load balancer itself mean no ready pod: check
      `kubectl -n ${var.namespace} get pods` and the pod's events (a spot
      node taken back restarts it). 500 from the app: `kubectl -n ${var.namespace} logs deploy/genshin-web`.
      During a canary, `deploy/gke/canary.sh rollback` sends everyone to stable.
    EOT
  }
}

resource "google_monitoring_alert_policy" "latency" {
  display_name = "${var.namespace}: slow responses"
  combiner     = "OR"
  severity     = "WARNING"
  conditions {
    display_name = "95th percentile latency above ${var.latency_p95_ms} ms (10 min, at least 20 requests)"
    condition_prometheus_query_language {
      query               = <<-EOT
        (histogram_quantile(0.95, sum by (le) (rate(${local.lb_latency}[10m]))) > ${var.latency_p95_ms})
        and on() (sum(increase(${local.lb_requests}[10m])) >= 20)
      EOT
      duration            = "600s"
      evaluation_interval = "60s"
    }
  }
  notification_channels = local.channels
  documentation {
    mime_type = "text/markdown"
    content   = "Pages and API calls are slow. Simulations share the node's CPU with the server: check the dashboard's CPU panel and `kubectl top pods -n ${var.namespace}`."
  }
}

resource "google_monitoring_alert_policy" "restarts" {
  display_name = "${var.namespace}: containers restarting"
  combiner     = "OR"
  severity     = "WARNING"
  conditions {
    display_name = "More than 2 container restarts in 30 minutes"
    condition_prometheus_query_language {
      query               = "sum by (pod_name, container_name) (increase(kubernetes_io:container_restart_count{${local.pod_filter}}[30m])) > 2"
      duration            = "0s"
      evaluation_interval = "60s"
    }
  }
  notification_channels = local.channels
  documentation {
    mime_type = "text/markdown"
    content   = "A container keeps restarting (crash, failed liveness probe or out of memory). `kubectl -n ${var.namespace} describe pod` shows the last state and reason."
  }
}

# One dashboard: what visitors see (requests, errors, latency at the load
# balancer), then what the pods use, then restarts.
resource "google_monitoring_dashboard" "site" {
  dashboard_json = jsonencode({
    displayName = "${var.namespace} site"
    mosaicLayout = {
      columns = 12
      tiles = [
        for i, t in concat(
          [for t in [
            { title = "Requests per second by response class", queries = ["sum by (response_code_class) (rate(${local.lb_requests}[5m]))"] },
            { title = "5xx share of requests", queries = ["sum(rate(${local.lb_5xx}[5m])) / sum(rate(${local.lb_requests}[5m]))"] },
            { title = "Latency p50 and p95 (ms)", queries = [for q in [0.5, 0.95] : "histogram_quantile(${q}, sum by (le) (rate(${local.lb_latency}[5m])))"] },
            { title = "Pod CPU (cores)", queries = ["sum by (pod_name, container_name) (rate(kubernetes_io:container_cpu_core_usage_time{${local.pod_filter}}[5m]))"] },
            { title = "Pod memory (bytes)", queries = ["sum by (pod_name, container_name) (kubernetes_io:container_memory_used_bytes{${local.pod_filter},memory_type=\"non-evictable\"})"] },
            { title = "Container restarts (30 min)", queries = ["sum by (pod_name, container_name) (increase(kubernetes_io:container_restart_count{${local.pod_filter}}[30m]))"] },
            ] : { title = t.title, sets = [for q in t.queries : { prometheusQuery = q }] }
          ],
          # Per site: SLO compliance over the 28-day window, and the share of
          # the error budget still left (1 = untouched, 0 or below = spent).
          flatten([for s in var.sites : [
            { title = "${s.name}: SLO compliance (28 days)", sets = [for k in ["availability", "latency"] : { timeSeriesFilter = { filter = "select_slo_compliance(\"${local.slo_by_kind[k][s.name].name}\")" } }] },
            { title = "${s.name}: error budget remaining", sets = [for k in ["availability", "latency"] : { timeSeriesFilter = { filter = "select_slo_budget_fraction(\"${local.slo_by_kind[k][s.name].name}\")" } }] },
          ]]),
          ) : {
          xPos   = (i % 2) * 6
          yPos   = floor(i / 2) * 4
          width  = 6
          height = 4
          widget = {
            title = t.title
            xyChart = {
              dataSets = [for q in t.sets : {
                plotType        = "LINE"
                timeSeriesQuery = q
              }]
            }
          }
        }
      ]
    }
  })
}
