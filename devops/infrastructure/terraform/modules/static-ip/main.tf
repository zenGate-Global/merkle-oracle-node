# Static IP Address Module for GCP Applications
# Reserves a static external IP address for the application

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

resource "google_compute_address" "app" {
  name         = "${var.app_name}-ip-${var.environment}"
  project      = var.project_id
  region       = var.region
  address_type = "EXTERNAL"
  description  = "Static IP for ${var.app_name} ${var.environment}"

  labels = {
    app         = var.app_name
    environment = var.environment
    managed_by  = "terraform"
  }
}

output "address" {
  description = "The static IP address"
  value       = google_compute_address.app.address
}

output "name" {
  description = "The name of the address resource"
  value       = google_compute_address.app.name
}

output "self_link" {
  description = "The URI of the address resource"
  value       = google_compute_address.app.self_link
}
