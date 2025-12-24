# Health Check Module for GCP Applications
# Creates HTTP health check for managed instance groups

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

resource "google_compute_health_check" "app" {
  name                = "${var.app_name}-hc-${var.environment}"
  project             = var.project_id
  check_interval_sec  = var.check_interval_sec
  timeout_sec         = var.timeout_sec
  healthy_threshold   = var.healthy_threshold
  unhealthy_threshold = var.unhealthy_threshold

  http_health_check {
    port         = var.port
    request_path = var.request_path
  }
}

output "id" {
  description = "Health check ID"
  value       = google_compute_health_check.app.id
}

output "self_link" {
  description = "Health check self link"
  value       = google_compute_health_check.app.self_link
}

output "name" {
  description = "Health check name"
  value       = google_compute_health_check.app.name
}
