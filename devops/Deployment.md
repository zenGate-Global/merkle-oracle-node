# Merkle Oracle Node Deployment Guide

This guide covers the automated deployment pipeline for the Merkle Oracle Node to Google Cloud Run using GitHub Actions.

## Overview

The deployment system supports:
- **Tag-based deployments** with automatic staging deployment on tag creation
- **Manual deployments** to staging or production with environment selection
- **Environment variable configuration** instead of config.yaml files
- **Rollback capabilities** using previous Docker image tags
- **Separate environments** for staging and production

## Deployment Methods

### 1. Automatic Deployment (Tag-based)

When you create a new tag, the application automatically deploys to staging:

```bash
# Create and push a new tag
git tag v1.2.3
git push origin v1.2.3
```

This triggers the `deploy-gcp.yml` workflow and deploys to the staging environment.

### 2. Manual Deployment

Use the GitHub Actions UI to manually deploy:

1. Go to **Actions** → **Deploy to Google Cloud**
2. Click **Run workflow**
3. Select:
   - **Environment**: staging or production
   - **Tag**: The git tag to deploy (e.g., v1.2.3)
   - **Force deploy**: Whether to force deployment if tag exists

### 3. Rollback Deployment

To rollback to a previous version:

1. Go to **Actions** → **Rollback Google Cloud Deployment**
2. Click **Run workflow**
3. Select:
   - **Environment**: staging or production
   - **Rollback tag**: The tag to rollback to (e.g., v1.1.0)
   - **Confirm rollback**: Type "CONFIRM" to proceed

#### Projects
Set up separate Google Cloud projects:
- `merkle-oracle-node-staging` (staging)
- `merkle-oracle-node-production` (production)

#### Cloud SQL Instances
- Staging: `merkle-oracle-node-staging:europe-west1:merkle-oracle-node-staging-db`
- Production: `merkle-oracle-node-production:europe-west1:merkle-oracle-node-production-db`

### 2. GitHub Secrets Configuration

Add these secrets to your GitHub repository:

#### Staging Environment
```
GCP_PROJECT_ID_STAGING=merkle-oracle-node-staging
GCP_SA_KEY_STAGING=<service-account-json-key>
GCP_CLOUDSQL_INSTANCE_STAGING=merkle-oracle-node-staging:europe-west1:merkle-oracle-node-staging-db
```

#### Production Environment
```
GCP_PROJECT_ID_PROD=merkle-oracle-node-production
GCP_SA_KEY_PROD=<service-account-json-key>
GCP_CLOUDSQL_INSTANCE_PROD=merkle-oracle-node-production:europe-west1:merkle-oracle-node-production-db
```

### 3. Google Cloud Secret Manager

Create the required secrets in each environment. See [environment-variables.md](environment-variables.md) for details.

## Environment Configuration

### Staging Environment
- **Network**: Cardano Preview Testnet
- **Service Name**: `merkle-oracle-node-staging`
- **Domain**: Auto-generated Cloud Run URL
- **Database**: Staging Cloud SQL instance

### Production Environment
- **Network**: Cardano Mainnet
- **Service Name**: `merkle-oracle-node`
- **Domain**: Auto-generated Cloud Run URL (can be mapped to custom domain)
- **Database**: Production Cloud SQL instance

## Manual Deployment Commands

If you need to deploy manually without GitHub Actions:

### Build and Push
```bash
# Set variables
PROJECT_ID="merkle-oracle-node-staging"
TAG="v1.2.3"
IMAGE_NAME="gcr.io/${PROJECT_ID}/merkle-oracle-node"

# Build and push
docker build -f Dockerfile.cloudrun -t ${IMAGE_NAME}:${TAG} .
docker push ${IMAGE_NAME}:${TAG}
```

### Deploy to Cloud Run
```bash
gcloud run deploy merkle-oracle-node-staging \
  --image ${IMAGE_NAME}:${TAG} \
  --platform managed \
  --region europe-west1 \
  --allow-unauthenticated \
  --port 8080 \
  --memory 512Mi \
  --cpu 1 \
  --min-instances 1 \
  --max-instances 5 \
  --timeout 3600 \
  --add-cloudsql-instances merkle-oracle-node-staging:europe-west1:merkle-oracle-node-staging-db \
  --update-env-vars "PROFILE=staging,NETWORK=preview" \
  --update-secrets "DATABASE_URL=database-url-staging:latest" \
  --update-secrets "MNEMONIC=wallet-mnemonic-staging:latest" \
  --update-secrets "BLOCKFROST_API_KEY=blockfrost-api-key-staging:latest" \
  --update-secrets "MAESTRO_API_KEY=maestro-api-key-staging:latest" \
  --update-secrets "PINATA_JWT=pinata-jwt-staging:latest"
```

## Rollback Procedures

### Automated Rollback via GitHub Actions

1. **Identify the target version**: Check previous successful deployments
2. **Run rollback workflow**: Use the GitHub Actions rollback workflow
3. **Verify rollback**: Check service health and functionality

### Manual Rollback

```bash
# List available image tags
gcloud container images list-tags gcr.io/merkle-oracle-node-staging/merkle-oracle-node

# Rollback to specific tag
ROLLBACK_TAG="v1.1.0"
gcloud run deploy merkle-oracle-node-staging \
  --image gcr.io/merkle-oracle-node-staging/merkle-oracle-node:${ROLLBACK_TAG} \
  --region europe-west1
```

### Database Rollback Considerations

**Important**: Database migrations are not automatically rolled back. If a deployment includes database schema changes:

1. **Test rollback** in staging first
2. **Backup database** before production rollback
3. **Manual intervention** may be required for schema changes
