# Hybrid Deployment Architecture

## Overview

The Merkle Oracle Node uses a **hybrid deployment approach** that separates infrastructure management from application deployments:

- **Terraform** = Infrastructure (run rarely)
- **GitHub Actions** = Application deployments (run frequently)

## Architecture

### Infrastructure Layer (Terraform)
Manages the foundational GCP resources:
- Managed Instance Group (MIG)
- Base instance template
- Static IP addresses
- Firewall rules
- Health checks
- Service accounts
- IAM permissions

**When to use**: Only when changing infrastructure configuration (machine type, network, scaling, etc.)

### Application Layer (Pipeline)
Manages application deployments:
- Docker image building
- Image versioning (tags)
- Instance template updates
- Rolling deployments
- Health verification

**When to use**: Every time you deploy new application code

## How It Works

### 1. Infrastructure Setup (One-time / Rare)

```bash
cd devops/infrastructure/terraform/environments/staging

# Initialize Terraform
terraform init

# Review changes
terraform plan

# Apply infrastructure
terraform apply
```

**What this creates:**
- MIG with base configuration
- Instance template with `latest` tag placeholder
- All supporting infrastructure

**Note**: The `image_tag` is hardcoded to `"latest"` in Terraform - this is just a placeholder. The actual deployed image tag is managed entirely by the pipeline.

### 2. Application Deployment (Frequent)

```bash
# Trigger via GitHub Actions
# Go to: Actions → "Deploy to Staging" → Run workflow
# Enter tag: v1.0.3
```

**What the pipeline does:**

1. **Build Phase**:
   - Checks out code at specified tag
   - Builds Docker image
   - Pushes to Artifact Registry with tag

2. **Deploy Phase**:
   - Gets current instance template from MIG
   - Creates NEW instance template with updated image tag
   - Updates MIG to use new template
   - Performs rolling restart (zero downtime)
   - Cleans up old templates (keeps last 5)

3. **Verify Phase**:
   - Tests application health
   - Verifies Caddy proxy
   - Tests all HTTPS endpoints
   - Verifies CORS headers
   - Confirms deployed image version

## Key Benefits

### ✅ Separation of Concerns
- Infrastructure changes don't require redeployment
- Application deployments don't modify infrastructure
- Clear boundaries between infra and app

### ✅ Faster Deployments
- No Terraform plan/apply cycle for deployments
- Direct gcloud commands (~3-5 minutes vs ~10-15 minutes)
- No state locking issues

### ✅ Tag Management
- Image tags ONLY exist in pipeline and registry
- No drift between repo and deployed state
- Pipeline is source of truth for versions

### ✅ Zero Downtime
- Rolling updates with max-surge=1, max-unavailable=0
- Health checks before marking instances ready
- Automatic rollback on failure

## Deployment Flow Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    INFRASTRUCTURE LAYER                      │
│                      (Terraform - Rare)                      │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │   MIG    │  │ Firewall │  │Static IP │  │  Health  │   │
│  │          │  │  Rules   │  │          │  │  Check   │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Base Instance Template (image_tag = "latest")       │  │
│  │  - Startup script                                     │  │
│  │  - Machine type: e2-medium                           │  │
│  │  - Network configuration                             │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                              │
└─────────────────────────────────────────────────────────────┘
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                   APPLICATION LAYER                          │
│                 (GitHub Actions - Frequent)                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Build Image                                             │
│     └─> europe-west1-docker.pkg.dev/.../app:v1.0.3         │
│                                                              │
│  2. Create New Template                                     │
│     └─> Copy current template                               │
│     └─> Update with new image tag                          │
│     └─> merkle-oracle-node-template-staging-20251223...    │
│                                                              │
│  3. Update MIG                                              │
│     └─> Set new template                                    │
│     └─> Rolling restart (max-surge=1, max-unavailable=0)   │
│                                                              │
│  4. Verify                                                  │
│     └─> Health checks                                       │
│     └─> HTTPS endpoints                                     │
│     └─> CORS headers                                        │
│                                                              │
│  5. Cleanup                                                 │
│     └─> Delete old templates (keep last 5)                 │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## File Structure

```
Merkle-oracle-node/
└── merkle-oracle-node/
    ├── devops/
    │   └── infrastructure/
    │       └── terraform/
    │           └── environments/
    │               └── staging/
    │                   ├── main.tf              # Infrastructure config
    │                   ├── variables.tf         # NO image_tag variable
    │                   └── terraform.tfvars     # NO image_tag value
    │
    └── .github/
        └── workflows/
            └── deploy-staging.yml       # Application deployment
```

## Terraform Configuration

### What Changed

**Before (Problematic):**
```hcl
# terraform.tfvars
image_tag = "v1.0.2"  # Gets out of sync!

# main.tf
image_tag = var.image_tag  # Causes drift
```

**After (Hybrid Approach):**
```hcl
# terraform.tfvars
# image_tag removed entirely

# main.tf
image_tag = "latest"  # Placeholder only, not used in production
```

### Why This Works

The instance template created by Terraform is just a **base template**. When you deploy:

1. Pipeline reads the current template
2. Creates a NEW template with the new image tag
3. MIG uses the NEW template
4. Old template is eventually cleaned up

The Terraform-managed template with `"latest"` is never actually used for running instances - it's just the starting point.

## Common Operations

### Deploy New Version
```bash
# Via GitHub Actions
Actions → Deploy to Staging → Run workflow → Enter tag
```

### Rollback to Previous Version
```bash
# Just deploy the old tag
Actions → Deploy to Staging → Run workflow → Enter old tag (e.g., v1.0.2)
```

### Update Infrastructure
```bash
cd infrastructure/terraform/environments/staging
terraform apply
# Then redeploy application to pick up changes
```

### Scale MIG
```bash
# Update terraform.tfvars
target_size = 3

# Apply
terraform apply

# Application continues running, just more instances
```

### View Deployed Version
```bash
# Check instance template used by MIG
gcloud compute instance-groups managed describe merkle-oracle-node-mig-staging \
  --zone=europe-west1-b \
  --format="value(instanceTemplate)"

# The template name contains the timestamp of deployment
```

## Troubleshooting

### Issue: Deployment fails during template creation

**Solution**: Check that the current template exists and is accessible
```bash
gcloud compute instance-templates list --filter="name:merkle-oracle-node-template-staging-*"
```

### Issue: MIG not updating to new template

**Solution**: Manually trigger rolling restart
```bash
gcloud compute instance-groups managed rolling-action restart merkle-oracle-node-mig-staging \
  --zone=europe-west1-b
```

### Issue: Want to see what image is actually running

**Solution**: SSH to instance and check
```bash
gcloud compute ssh INSTANCE_NAME --zone=europe-west1-b --tunnel-through-iap
sudo docker inspect merkle-oracle-node --format='{{.Config.Image}}'
```

## Best Practices

1. **Tag Everything**: Always use semantic versioning (v1.0.3, v1.1.0, etc.)
2. **Test Locally**: Build and test images locally before deploying
3. **Monitor Deployments**: Watch GitHub Actions logs during deployment
4. **Keep Templates Clean**: Pipeline auto-cleans old templates (keeps last 5)
5. **Separate Concerns**: Use Terraform for infra, pipeline for apps
6. **Document Changes**: Update CHANGELOG when deploying new versions

## Migration from Old Approach

If you have existing deployments with Terraform-managed image tags:

1. ✅ Remove `image_tag` from `terraform.tfvars`
2. ✅ Remove `image_tag` variable from `variables.tf`
3. ✅ Set `image_tag = "latest"` in `main.tf` (placeholder)
4. ✅ Run `terraform apply` to update base template
5. ✅ Use new pipeline for all future deployments

The existing running instances will continue working. Next deployment will use the new approach.

## Summary

**Terraform** = Infrastructure foundation (MIG, networks, IAM)  
**Pipeline** = Application deployments (images, versions, updates)

This separation gives you:
- Faster deployments
- Clearer version management
- No state drift
- Industry-standard approach

Infrastructure changes are rare. Application deployments are frequent. This architecture optimizes for both.
