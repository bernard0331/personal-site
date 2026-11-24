# SLOs and error-budget burn-rate alerts for every site behind the Gateway.
#
# Each site gets a Cloud Monitoring custom service with two request-based
# SLOs, measured at the load balancer (what visitors saw, including a pod
# that's down, when the load balancer answers 502/503 itself):
#
#   availability   share of requests that are not 5xx
#   latency        share of requests answered within latency_threshold_ms
#
# A site's requests are picked out by the Gateway's backend service name,
# which GKE builds as gkegw1-<id>-<namespace>-<service>-<service port>-<hash>
# (seen live: gkegw1-kna0-genshin-genshin-web-80-...). The
# match also covers the canary (<service>-canary), since visitors see its
# errors too.
#
# Burn-rate alerts follow the multi-window pattern from Google's SRE
# workbook. Burn rate is how fast the budget is being spent: 1 spends exactly
# the whole budget over the SLO window. Each alert needs a long window (is
# it real?) AND a short one (is it still happening?), so it fires quickly
# and clears quickly once the errors stop.
#
#   fast  14.4x over 1h and 5m    2% of a 28-day budget in an hour   page
#   slow   6x   over 6h and 30m   5% of the budget in six hours      ticket
#
# The alerts use Cloud Monitoring's own select_slo_burn_rate rather than
# PromQL: the threshold conditions it needs can be combined with AND in one
# policy, which also carries a minimum-traffic condition, so a quiet personal
# site doesn't page on two failed requests.

locals {
  lb_filter = {
    for s in var.sites : s.name => join(" AND ", [
      "resource.type=\"https_lb_rule\"",
      "resource.label.backend_target_name=monitoring.regex.full_match(\".*-${s.namespace}-${s.name}-.*\")",
    ])
  }
  requests_metric = "metric.type=\"loadbalancing.googleapis.com/https/request_count\""
  latency_metric  = "metric.type=\"loadbalancing.googleapis.com/https/total_latencies\""

  slo_by_kind = {
    availability = google_monitoring_slo.availability
    latency      = google_monitoring_slo.latency
  }

  burn_windows = {
    fast = { long = 3600, short = 300, rate = 14.4, severity = "CRITICAL", label = "1h and 5m", act = "Page: at this rate the whole 28-day budget is gone in about two days." }
    slow = { long = 21600, short = 1800, rate = 6, severity = "WARNING", label = "6h and 30m", act = "Ticket: at this rate the budget is gone in under five days." }
  }

  slos = merge([
    for s in var.sites : {
      "${s.name}/availability" = {
        site    = s
        slo     = google_monitoring_slo.availability[s.name]
        target  = var.availability_target
        meaning = "requests that are not 5xx"
        runbook = "5xx at the load balancer. 502/503 from the load balancer itself mean no ready pod: `kubectl -n ${s.namespace} get pods` and its events (a spot node taken back restarts it). 500 from the app: `kubectl -n ${s.namespace} logs deploy/${s.name}`. During a canary, `deploy/gke/canary.sh rollback` sends everyone to stable."
      }
      "${s.name}/latency" = {
        site    = s
        slo     = google_monitoring_slo.latency[s.name]
        target  = var.latency_target
        meaning = "requests answered within ${var.latency_threshold_ms} ms"
        runbook = "Requests are slow at the load balancer. Simulations share the node's CPU with the server: check the dashboard's CPU panel and `kubectl top pods -n ${s.namespace}`. During a canary, compare the two versions and roll back if the canary is the slow one."
      }
    }
  ]...)

  burn_alerts = merge([
    for k, v in local.slos : {
      for w, win in local.burn_windows : "${k}/${w}" => merge(v, win, { window = w })
    }
  ]...)
}

resource "google_monitoring_custom_service" "site" {
  for_each     = { for s in var.sites : s.name => s }
  service_id   = "site-${each.key}"
  display_name = "${each.key} (namespace ${each.value.namespace})"
}

resource "google_monitoring_slo" "availability" {
  for_each            = { for s in var.sites : s.name => s }
  service             = google_monitoring_custom_service.site[each.key].service_id
  slo_id              = "availability"
  display_name        = "${each.key}: ${var.availability_target * 100}% of requests not 5xx (28 days)"
  goal                = var.availability_target
  rolling_period_days = 28

  request_based_sli {
    good_total_ratio {
      total_service_filter = join(" AND ", [local.requests_metric, local.lb_filter[each.key]])
      bad_service_filter   = join(" AND ", [local.requests_metric, local.lb_filter[each.key], "metric.label.response_code_class=500"])
    }
  }
}

resource "google_monitoring_slo" "latency" {
  for_each            = { for s in var.sites : s.name => s }
  service             = google_monitoring_custom_service.site[each.key].service_id
  slo_id              = "latency"
  display_name        = "${each.key}: ${var.latency_target * 100}% of requests within ${var.latency_threshold_ms} ms (28 days)"
  goal                = var.latency_target
  rolling_period_days = 28

  request_based_sli {
    distribution_cut {
      distribution_filter = join(" AND ", [local.latency_metric, local.lb_filter[each.key]])
      range {
        max = var.latency_threshold_ms
      }
    }
  }
}

resource "google_monitoring_alert_policy" "burn" {
  for_each     = local.burn_alerts
  display_name = "${each.value.site.name}: ${split("/", each.key)[1]} budget burning (${each.value.window})"
  combiner     = "AND"
  severity     = each.value.severity

  conditions {
    display_name = "Burn rate above ${each.value.rate}x over ${each.value.long / 3600}h"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${each.value.slo.name}\", \"${each.value.long}s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = each.value.rate
      duration        = "0s"
    }
  }

  conditions {
    display_name = "Burn rate above ${each.value.rate}x over ${each.value.short / 60} min"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${each.value.slo.name}\", \"${each.value.short}s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = each.value.rate
      duration        = "0s"
    }
  }

  conditions {
    display_name = "At least ${var.burn_min_requests} requests over ${each.value.long / 3600}h"
    condition_threshold {
      filter          = join(" AND ", [local.requests_metric, local.lb_filter[each.value.site.name]])
      comparison      = "COMPARISON_GT"
      threshold_value = var.burn_min_requests - 1
      duration        = "0s"
      aggregations {
        alignment_period     = "${each.value.long}s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }

  notification_channels = local.channels
  documentation {
    mime_type = "text/markdown"
    content   = <<-EOT
      The ${each.value.site.name} SLO "${each.value.target * 100}% of ${each.value.meaning}" is spending its
      28-day error budget at more than ${each.value.rate}x the sustainable rate (over ${each.value.label}).
      ${each.value.act}

      ${each.value.runbook}

      Remaining budget: the "error budget remaining" panel on the site dashboard.
    EOT
  }
}
