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

variable "port" {
  type        = number
  default     = 8080
  description = "Port to check"
}

variable "request_path" {
  type        = string
  default     = "/healthcheck"
  description = "HTTP path for health check"
}

variable "check_interval_sec" {
  type        = number
  default     = 30
  description = "How often to check (seconds)"
}

variable "timeout_sec" {
  type        = number
  default     = 10
  description = "Timeout for each check (seconds)"
}

variable "healthy_threshold" {
  type        = number
  default     = 2
  description = "Consecutive successes to mark healthy"
}

variable "unhealthy_threshold" {
  type        = number
  default     = 3
  description = "Consecutive failures to mark unhealthy"
}
