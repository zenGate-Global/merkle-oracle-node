# Staging Environment Configuration
# Merkle Oracle Node - Ubuntu MIG Infrastructure

terraform {
  required_version = ">= 1.5.0"

  backend "gcs" {
    bucket = "zengate-terraform-state-merkle-oracle"
    prefix = "merkle-oracle/staging"
  }

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Static IP Address
module "static_ip" {
  source = "../../modules/static-ip"

  app_name    = var.app_name
  environment = var.environment
  project_id  = var.project_id
  region      = var.region
}

# Service Account
module "service_account" {
  source = "../../modules/service-account"

  app_name    = var.app_name
  environment = var.environment
  project_id  = var.project_id
}

# Health Check
module "health_check" {
  source = "../../modules/health-check"

  app_name     = var.app_name
  environment  = var.environment
  project_id   = var.project_id
  port         = var.app_port
  request_path = var.health_check_path
}

# Instance Template
module "instance_template" {
  source = "../../modules/instance-template"

  app_name              = var.app_name
  environment           = var.environment
  project_id            = var.project_id
  region                = var.region
  machine_type          = var.machine_type
  image_tag             = "latest"  # Placeholder - actual tag managed by pipeline
  secret_name           = var.secret_name
  domain                = var.domain
  app_port              = var.app_port
  service_account_email = module.service_account.email
  network_tags          = ["${var.app_name}-web", "allow-health-check"]
  static_ip_address     = module.static_ip.address
}

# Managed Instance Group
module "mig" {
  source = "../../modules/mig"

  app_name             = var.app_name
  environment          = var.environment
  project_id           = var.project_id
  zone                 = var.zone
  target_size          = var.target_size
  instance_template_id = module.instance_template.self_link
  health_check_id      = module.health_check.id
}

# Firewall Rules
module "firewall" {
  source = "../../modules/firewall"

  app_name    = var.app_name
  environment = var.environment
  project_id  = var.project_id
  app_port    = var.app_port
}

# Outputs
output "static_ip_address" {
  value       = module.static_ip.address
  description = "Static IP address for the application"
}

output "service_account_email" {
  value       = module.service_account.email
  description = "Service account email"
}

output "instance_template_name" {
  value       = module.instance_template.name
  description = "Instance template name"
}

output "mig_name" {
  value       = module.mig.name
  description = "Managed instance group name"
}

output "health_check_name" {
  value       = module.health_check.name
  description = "Health check name"
}

output "domain" {
  value       = var.domain
  description = "Application domain"
}

output "image_tag" {
  value       = var.image_tag
  description = "Current deployed image tag"
}
