# GitHub Actions Workflows

## Active Workflows

### `deploy-mig-staging.yml` - Production Deployment Pipeline

**Purpose**: Deploy application to GCP Managed Instance Group (MIG) with Terraform

**Trigger**: Manual (`workflow_dispatch`)

**Required Input**:
- `tag`: Docker image tag to deploy (e.g., `v1.0.2`)

**What it does**:
1. Builds Docker image from specified tag
2. Pushes to Google Artifact Registry
3. Updates Terraform configuration with new image tag
4. Applies Terraform changes to update instance template
5. Performs rolling restart of MIG instances
6. Runs comprehensive health checks
7. Verifies HTTPS endpoints and CORS headers

**Environment**: `staging`

**Required Secrets**:
- `WIF_PROVIDER`: Workload Identity Federation provider
- `WIF_SERVICE_ACCOUNT`: Service account email

**Duration**: ~10-15 minutes

**Usage**:
```bash
# Go to Actions tab in GitHub
# Select "Deploy to GCP MIG Staging"
# Click "Run workflow"
# Enter tag: v1.0.2
# Click "Run workflow"
```

---

### Other Workflows

- **`go-test.yml`**: Runs Go tests on pull requests
- **`golangci-lint.yml`**: Runs linting checks
- **`conventional-commits.yml`**: Validates commit messages
- **`release.yml`**: Creates GitHub releases
- **`clear_cache.yaml`**: Clears GitHub Actions cache

## Deployment Flow

```
Tag Release (v1.0.2)
    ↓
Trigger Workflow (Manual)
    ↓
Build & Push Image
    ↓
Update Terraform Config
    ↓
Apply Infrastructure Changes
    ↓
Rolling Restart MIG
    ↓
Health Checks & Verification
    ↓
Deployment Summary
```

## Infrastructure Details

- **Project**: `merkle-oracle-staging`
- **MIG**: `merkle-oracle-node-mig-staging`
- **Domain**: `https://merkle-staging4.zengate-dev.com`
- **Registry**: `europe-west1-docker.pkg.dev/merkle-oracle-staging/merkle-oracle-node`

## Endpoints Verified

After deployment, these endpoints are automatically tested:

- ✅ `/healthcheck` - Application health
- ✅ `/objects` - API functionality
- ✅ `/docs` - API documentation
- ✅ `/docs/swagger.json` - OpenAPI spec
- ✅ CORS headers - Cross-origin support

## Rollback

To rollback to a previous version:
1. Trigger workflow with previous tag
2. Wait for deployment to complete
3. Verify endpoints

## Monitoring

View deployment status:
- GitHub Actions: Real-time logs
- GCP Console: Instance health and metrics
- Application logs: Via SSH to instances

## Notes

- Old `deploy-gcp.yml` workflow has been removed (single-instance deployment)
- New workflow uses MIG for production-ready deployments
- Terraform manages all infrastructure
- Zero-downtime deployments via rolling updates
- Automatic health checks and rollback on failure
