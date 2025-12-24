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

variable "network" {
  type        = string
  default     = "default"
  description = "VPC network"
}

variable "app_port" {
  type        = number
  default     = 8080
  description = "Application port for health checks"
}
