# Deployment Guide - Merkle Oracle Node

## Overview

The Merkle Oracle Node uses a production-ready CI/CD pipeline that deploys to Google Cloud Platform (GCP) using Managed Instance Groups (MIG) with Terraform infrastructure as code.

## Architecture

- **Infrastructure**: Terraform-managed GCP resources
- **Compute**: Managed Instance Group (MIG) with auto-healing
- **Container Registry**: Google Artifact Registry
- **Reverse Proxy**: Caddy with automatic HTTPS (Let's Encrypt)
- **Database**: Cloud SQL PostgreSQL (private IP)
- **Deployment**: GitHub Actions with Workload Identity Federation

## Environments

### Staging
- **Project**: `merkle-oracle-staging`
- **Region**: `europe-west1`
- **Zone**: `europe-west1-b`
- **Domain**: `https://merkle-staging4.zengate-dev.com`
- **MIG**: `merkle-oracle-node-mig-staging`

## Deployment Pipeline

### Workflow: `deploy-mig-staging.yml`

The deployment pipeline consists of 4 jobs:

1. **Build**: Builds and pushes Docker image to Artifact Registry
2. **Deploy**: Updates Terraform configuration and performs rolling restart of MIG
3. **Verify**: Runs comprehensive health checks on deployed instances
4. **Summary**: Creates deployment summary and notifies on failure

### Triggering a Deployment

Deployments are triggered manually via GitHub Actions:

```bash
# Navigate to GitHub Actions
https://github.com/YOUR_ORG/Merkle-oracle-node/actions

# Select "Deploy to GCP MIG Staging" workflow
# Click "Run workflow"
# Enter the tag (e.g., v1.0.2)
# Click "Run workflow"
```

### What Happens During Deployment

1. **Build Phase**:
   - Checks out code at specified tag
   - Authenticates to GCP using Workload Identity
   - Builds Docker image
   - Pushes to Artifact Registry with tag and `latest`

2. **Deploy Phase**:
   - Updates `terraform.tfvars` with new image tag
   - Runs `terraform plan` and `terraform apply`
   - Performs rolling restart of MIG instances
   - Waits for MIG to stabilize

3. **Verify Phase**:
   - Tests application health endpoint
   - Verifies Caddy reverse proxy is running
   - Tests all HTTPS endpoints
   - Verifies CORS headers are present

4. **Summary Phase**:
   - Creates detailed deployment summary
   - Notifies on failure

## Prerequisites

### GitHub Secrets (Repository Level)

Required secrets for the `staging` environment:

- `WIF_PROVIDER`: Workload Identity Federation provider
  ```
  projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/POOL_NAME/providers/PROVIDER_NAME
  ```

- `WIF_SERVICE_ACCOUNT`: Service account email
  ```
  github-actions@merkle-oracle-staging.iam.gserviceaccount.com
  ```

### GCP Setup

1. **Workload Identity Federation** configured for GitHub Actions
2. **Service Account** with permissions:
   - Artifact Registry Writer
   - Compute Instance Admin
   - Service Account User
   - Storage Object Viewer (for Terraform state)

3. **Artifact Registry** repository:
   ```bash
   gcloud artifacts repositories create merkle-oracle-node \
     --repository-format=docker \
     --location=europe-west1 \
     --project=merkle-oracle-staging
   ```

4. **Terraform State Bucket**:
   ```bash
   gsutil mb -p merkle-oracle-staging -l europe-west1 \
     gs://zengate-terraform-state-merkle-oracle/
   ```

## Infrastructure Management

### Terraform Configuration

Location: `devops/infrastructure/terraform/environments/staging/`

Key files:
- `main.tf`: Main Terraform configuration
- `variables.tf`: Variable definitions
- `terraform.tfvars`: Environment-specific values

### Manual Terraform Operations

```bash
cd infrastructure/terraform/environments/staging

# Initialize
terraform init

# Plan changes
terraform plan

# Apply changes
terraform apply

# Destroy (use with caution)
terraform destroy
```

### Updating Configuration

To update infrastructure configuration:

1. Edit `terraform.tfvars` or module configurations
2. Run deployment pipeline (it will apply Terraform changes)
3. Or manually run `terraform apply` locally

## Application Configuration

### Secret Manager

Application configuration is stored in GCP Secret Manager:

```bash
# View current config
gcloud secrets versions access latest \
  --secret=merkle-oracle-staging-config \
  --project=merkle-oracle-staging

# Update config
gcloud secrets versions add merkle-oracle-staging-config \
  --data-file=config.yaml \
  --project=merkle-oracle-staging
```

After updating secrets, restart instances:
```bash
gcloud compute instance-groups managed rolling-action restart \
  merkle-oracle-node-mig-staging \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging
```

## Monitoring and Debugging

### View Instance Logs

```bash
# List instances in MIG
gcloud compute instance-groups managed list-instances \
  merkle-oracle-node-mig-staging \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging

# SSH to instance
gcloud compute ssh INSTANCE_NAME \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging \
  --tunnel-through-iap

# View application logs
sudo docker logs merkle-oracle-node --tail 100 -f

# View Caddy logs
sudo docker logs merkle-oracle-node-caddy --tail 100 -f

# Check systemd services
sudo systemctl status merkle-oracle-node.service
sudo systemctl status merkle-oracle-node-caddy.service
```

### Health Checks

```bash
# Application health
curl https://merkle-staging4.zengate-dev.com/healthcheck

# Objects endpoint
curl https://merkle-staging4.zengate-dev.com/objects?limit=5

# API documentation
curl https://merkle-staging4.zengate-dev.com/docs

# Swagger spec
curl https://merkle-staging4.zengate-dev.com/docs/swagger.json
```

### MIG Status

```bash
# Check MIG status
gcloud compute instance-groups managed describe \
  merkle-oracle-node-mig-staging \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging

# View MIG events
gcloud compute instance-groups managed list-errors \
  merkle-oracle-node-mig-staging \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging
```

## Rollback Procedure

### Quick Rollback

If a deployment fails, rollback to previous version:

```bash
# Trigger deployment with previous tag
# Go to GitHub Actions and run workflow with old tag (e.g., v1.0.1)
```

### Manual Rollback

```bash
# Update terraform.tfvars with previous image tag
cd infrastructure/terraform/environments/staging
sed -i 's/image_tag.*=.*/image_tag = "v1.0.1"/' terraform.tfvars

# Apply changes
terraform apply -auto-approve

# Restart instances
gcloud compute instance-groups managed rolling-action restart \
  merkle-oracle-node-mig-staging \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging
```

## Scaling

### Manual Scaling

```bash
# Scale up
gcloud compute instance-groups managed resize \
  merkle-oracle-node-mig-staging \
  --size=2 \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging

# Scale down
gcloud compute instance-groups managed resize \
  merkle-oracle-node-mig-staging \
  --size=1 \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging
```

### Autoscaling (Future)

To enable autoscaling, add to Terraform configuration:

```hcl
resource "google_compute_autoscaler" "app" {
  name   = "${var.app_name}-autoscaler-${var.environment}"
  zone   = var.zone
  target = module.mig.instance_group_manager_id

  autoscaling_policy {
    max_replicas    = 5
    min_replicas    = 1
    cooldown_period = 60

    cpu_utilization {
      target = 0.7
    }
  }
}
```

## Troubleshooting

### Common Issues

#### 1. Application Not Starting

```bash
# Check startup script logs
gcloud compute ssh INSTANCE_NAME \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging \
  --tunnel-through-iap \
  --command="sudo journalctl -u google-startup-scripts.service -n 100"

# Check application logs
sudo docker logs merkle-oracle-node --tail 100
```

#### 2. HTTPS Certificate Issues

```bash
# Check Caddy logs
sudo docker logs merkle-oracle-node-caddy --tail 100

# Verify DNS
dig merkle-staging4.zengate-dev.com

# Check certificate
curl -vI https://merkle-staging4.zengate-dev.com 2>&1 | grep -i certificate
```

#### 3. Database Connection Issues

```bash
# Verify private IP connectivity
gcloud compute ssh INSTANCE_NAME \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging \
  --tunnel-through-iap \
  --command="nc -zv 10.81.32.3 5432"

# Check application config
sudo cat /opt/merkle-oracle-node/config/config.yaml | grep -A5 storage
```

#### 4. MIG Not Updating

```bash
# Force recreation of instances
gcloud compute instance-groups managed recreate-instances \
  merkle-oracle-node-mig-staging \
  --instances=INSTANCE_NAME \
  --zone=europe-west1-b \
  --project=merkle-oracle-staging
```

## Best Practices

1. **Always use tags**: Deploy specific version tags, not branches
2. **Test locally first**: Build and test Docker images locally before deploying
3. **Monitor deployments**: Watch GitHub Actions logs during deployment
4. **Verify after deployment**: Check all endpoints after deployment completes
5. **Keep terraform.tfvars in sync**: Ensure local and deployed configs match
6. **Use rolling updates**: Never delete all instances at once
7. **Backup before major changes**: Export current configuration before updates

## Security Notes

- All secrets are stored in GCP Secret Manager
- Workload Identity Federation eliminates need for service account keys
- HTTPS is enforced via Caddy with automatic certificate renewal
- Database uses private IP connectivity (no public access)
- SSH access requires IAP tunnel (no public SSH)
- CORS headers configured for cross-origin requests

## Cost Optimization

Current configuration:
- **Compute**: 1x e2-medium instance (~$24/month)
- **Database**: Cloud SQL instance (varies by size)
- **Storage**: Artifact Registry and Cloud Storage (minimal)
- **Network**: Egress charges (varies by traffic)
- **No Cloud Logging costs**: Using local Docker logs only

## Support

For issues or questions:
1. Check GitHub Actions logs
2. Review instance logs via SSH
3. Check Terraform state for infrastructure issues
4. Verify GCP quotas and permissions
