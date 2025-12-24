# Service Account Module for GCP Applications
# Creates a service account with necessary permissions for running containerized apps

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

resource "google_service_account" "app" {
  account_id   = "${var.app_name}-sa-${var.environment}"
  display_name = "${var.app_name} Service Account (${var.environment})"
  project      = var.project_id
}

# Secret Manager access - read application config
resource "google_project_iam_member" "secret_accessor" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.app.email}"
}

# Artifact Registry access - pull container images
resource "google_project_iam_member" "artifact_reader" {
  project = var.project_id
  role    = "roles/artifactregistry.reader"
  member  = "serviceAccount:${google_service_account.app.email}"
}

# Cloud Logging - write logs
resource "google_project_iam_member" "log_writer" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.app.email}"
}

# Cloud Monitoring - write metrics
resource "google_project_iam_member" "metric_writer" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.app.email}"
}

output "email" {
  description = "Service account email"
  value       = google_service_account.app.email
}

output "id" {
  description = "Service account ID"
  value       = google_service_account.app.id
}
