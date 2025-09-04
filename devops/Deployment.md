# Merkle Oracle Node Deployment Guide

## Manual Build and Push

To build and push the Docker image manually:

```bash
# Build the Docker image
docker build -f Dockerfile.cloudrun -t gcr.io/merkle-oracle-node-staging/merkle-oracle-node .

# Push the image to Google Container Registry
docker push gcr.io/merkle-oracle-node-staging/merkle-oracle-node
```

## Managing Secrets

### Adding a New Secret

To add a new secret from your configuration file:

1. First, pull the existing secret from Google Cloud Secret Manager
2. Make necessary changes to the configuration file
3. Add the updated configuration as a new version:

```bash
gcloud secrets versions add merkle-oracle-config --data-file=config.example.yaml
```

## Deployment

### Deploy Command

```bash
gcloud run deploy $SERVICE_NAME \
  --image gcr.io/merkle-oracle-node-staging/merkle-oracle-node \
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
  --set-secrets="/etc/config/config.yaml=merkle-oracle-config:latest"
```