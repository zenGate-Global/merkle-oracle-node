# Instance Template Module for GCP Applications
# Creates a compute instance template for managed instance groups

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

resource "google_compute_instance_template" "app" {
  name_prefix  = "${var.app_name}-template-${var.environment}-"
  machine_type = var.machine_type
  region       = var.region
  project      = var.project_id

  disk {
    source_image = var.source_image
    auto_delete  = true
    boot         = true
    disk_size_gb = var.disk_size_gb
    disk_type    = "pd-ssd"
  }

  network_interface {
    network    = var.network
    subnetwork = var.subnetwork

    dynamic "access_config" {
      for_each = var.external_ip ? [1] : []
      content {
        nat_ip = var.static_ip_address
      }
    }
  }

  metadata = {
    startup-script = templatefile("${path.module}/../../scripts/ubuntu-startup.sh.tpl", {
      app_name    = var.app_name
      project_id  = var.project_id
      region      = var.region
      image_tag   = var.image_tag
      secret_name = var.secret_name
      domain      = var.domain
      app_port    = var.app_port
    })
    enable-oslogin = "TRUE"
  }

  service_account {
    email  = var.service_account_email
    scopes = ["cloud-platform"]
  }

  tags = var.network_tags

  labels = {
    app         = var.app_name
    environment = var.environment
    managed_by  = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

output "id" {
  description = "Instance template ID"
  value       = google_compute_instance_template.app.id
}

output "self_link" {
  description = "Instance template self link"
  value       = google_compute_instance_template.app.self_link
}

output "name" {
  description = "Instance template name"
  value       = google_compute_instance_template.app.name
}

output "self_link_unique" {
  description = "Instance template self link with unique suffix"
  value       = google_compute_instance_template.app.self_link_unique
}
