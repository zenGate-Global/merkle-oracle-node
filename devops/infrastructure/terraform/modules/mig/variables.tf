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

variable "zone" {
  type        = string
  description = "GCP zone"
}

variable "target_size" {
  type        = number
  default     = 1
  description = "Number of instances in the group"
}

variable "instance_template_id" {
  type        = string
  description = "Instance template ID to use"
}

variable "health_check_id" {
  type        = string
  default     = null
  description = "Health check ID for auto-healing (optional)"
}

variable "initial_delay_sec" {
  type        = number
  default     = 300
  description = "Time to wait before checking health after instance creation"
}

variable "max_surge" {
  type        = number
  default     = 1
  description = "Max instances to add during rolling update"
}

variable "max_unavailable" {
  type        = number
  default     = 0
  description = "Max instances that can be unavailable during update"
}
