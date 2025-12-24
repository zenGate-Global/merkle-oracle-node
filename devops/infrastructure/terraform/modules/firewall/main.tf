# Firewall Rules Module for GCP Applications
# Creates necessary firewall rules for web applications with Caddy

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

# Allow HTTP/HTTPS from anywhere (for Caddy)
resource "google_compute_firewall" "allow_http_https" {
  name    = "${var.app_name}-allow-http-https-${var.environment}"
  network = var.network
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["80", "443"]
  }

  source_ranges = ["0.0.0.0/0"]
  target_tags   = ["${var.app_name}-web"]
}

# Allow health checks from GCP health check IP ranges
resource "google_compute_firewall" "allow_health_check" {
  name    = "${var.app_name}-allow-health-check-${var.environment}"
  network = var.network
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = [tostring(var.app_port)]
  }

  # GCP health check IP ranges
  source_ranges = ["130.211.0.0/22", "35.191.0.0/16"]
  target_tags   = ["allow-health-check"]
}

# Allow SSH via IAP
resource "google_compute_firewall" "allow_iap_ssh" {
  name    = "${var.app_name}-allow-iap-ssh-${var.environment}"
  network = var.network
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  # IAP IP range
  source_ranges = ["35.235.240.0/20"]
  target_tags   = ["${var.app_name}-web"]
}

output "http_https_rule_name" {
  value = google_compute_firewall.allow_http_https.name
}

output "health_check_rule_name" {
  value = google_compute_firewall.allow_health_check.name
}

output "iap_ssh_rule_name" {
  value = google_compute_firewall.allow_iap_ssh.name
}
