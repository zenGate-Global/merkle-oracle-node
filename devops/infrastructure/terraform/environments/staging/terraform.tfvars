# Staging Environment Variables
# Merkle Oracle Node

# Project Configuration
project_id  = "merkle-oracle-staging"
region      = "europe-west1"
zone        = "europe-west1-b"
environment = "staging"

# Application Configuration
app_name          = "merkle-oracle-node"
secret_name       = "merkle-oracle-staging-config"
domain            = "merkle-staging4.zengate-dev.com"
app_port          = 8080
health_check_path = "/healthcheck"

# Instance Configuration
machine_type = "e2-medium"
target_size  = 1
