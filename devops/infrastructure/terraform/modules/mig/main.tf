# Managed Instance Group Module for GCP Applications
# Creates a MIG with auto-healing and rolling update policies

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

resource "google_compute_instance_group_manager" "app" {
  name               = "${var.app_name}-mig-${var.environment}"
  base_instance_name = "${var.app_name}-${var.environment}"
  zone               = var.zone
  project            = var.project_id
  target_size        = var.target_size

  version {
    instance_template = var.instance_template_id
  }

  dynamic "auto_healing_policies" {
    for_each = var.health_check_id != null ? [1] : []
    content {
      health_check      = var.health_check_id
      initial_delay_sec = var.initial_delay_sec
    }
  }

  update_policy {
    type                           = "PROACTIVE"
    minimal_action                 = "REPLACE"
    most_disruptive_allowed_action = "REPLACE"
    max_surge_fixed                = var.max_surge
    max_unavailable_fixed          = var.max_unavailable
    replacement_method             = "SUBSTITUTE"
  }

  lifecycle {
    create_before_destroy = true
  }
}

output "instance_group" {
  description = "Instance group URL"
  value       = google_compute_instance_group_manager.app.instance_group
}

output "self_link" {
  description = "MIG self link"
  value       = google_compute_instance_group_manager.app.self_link
}

output "name" {
  description = "MIG name"
  value       = google_compute_instance_group_manager.app.name
}
