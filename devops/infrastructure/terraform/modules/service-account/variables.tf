variable "app_name" {
  type        = string
  description = "Application name"
}

variable "environment" {
  type        = string
  description = "Environment (staging, production)"
}

variable "project_id" {
  type        = string
  description = "GCP project ID"
}
