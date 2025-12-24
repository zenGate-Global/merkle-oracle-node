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

variable "region" {
  type        = string
  description = "GCP region"
}

variable "machine_type" {
  type        = string
  default     = "e2-medium"
  description = "VM machine type"
}

variable "source_image" {
  type        = string
  default     = "ubuntu-os-cloud/ubuntu-2204-lts"
  description = "Boot disk source image"
}

variable "disk_size_gb" {
  type        = number
  default     = 100
  description = "Boot disk size in GB"
}

variable "network" {
  type        = string
  default     = "default"
  description = "VPC network"
}

variable "subnetwork" {
  type        = string
  default     = null
  description = "VPC subnetwork"
}

variable "external_ip" {
  type        = bool
  default     = true
  description = "Whether to assign external IP (required for Caddy ACME)"
}

variable "image_tag" {
  type        = string
  description = "Docker image tag to deploy"
}

variable "secret_name" {
  type        = string
  description = "Secret Manager secret name for app config"
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

variable "service_account_email" {
  type        = string
  description = "Service account email for the instance"
}

variable "network_tags" {
  type        = list(string)
  default     = []
  description = "Network tags for firewall rules"
}

variable "static_ip_address" {
  type        = string
  default     = null
  description = "Static IP address to assign to the instance (optional)"
}
