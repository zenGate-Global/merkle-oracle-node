variable "project_id" {
  type        = string
  description = "GCP project ID"
}

variable "region" {
  type        = string
  description = "GCP region"
}

variable "zone" {
  type        = string
  description = "GCP zone"
}

variable "environment" {
  type        = string
  description = "Environment name"
}

variable "app_name" {
  type        = string
  description = "Application name"
}

variable "secret_name" {
  type        = string
  description = "Secret Manager secret name"
}

variable "domain" {
  type        = string
  description = "Public domain for HTTPS"
}

variable "app_port" {
  type        = number
  default     = 8080
  description = "Application port"
}

variable "health_check_path" {
  type        = string
  default     = "/healthcheck"
  description = "Health check endpoint path"
}

variable "machine_type" {
  type        = string
  default     = "e2-medium"
  description = "VM machine type"
}

variable "target_size" {
  type        = number
  default     = 1
  description = "Number of instances"
}
