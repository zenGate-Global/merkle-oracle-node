# Merkle Oracle Node - Deployment Management Guide

## Quick Reference

| Item | Value |
|------|-------|
| **GCP Project** | `palm-portal-staging` |
| **Region** | `europe-west1` |
| **Zone** | `europe-west1-b` |
| **Instance Name** | `merkle-oracle-staging` |
| **Image Name** | `merkle-oracle-node` |
| **Domain** | `merkle-oracle.zengate-dev.com` |
| **Secret Name** | `merkle-oracle-prod-config` |

---

## 1. Update Secrets

The application config is stored in **GCP Secret Manager**.

### Via GCP Console
1. Go to [Secret Manager](https://console.cloud.google.com/security/secret-manager?project=palm-portal-staging)
2. Click on `merkle-oracle-prod-config`
3. Click **+ New Version**
4. Paste updated config YAML content
5. Click **Add New Version**
6. Restart the service (see below)

### Via gcloud CLI
```bash
# Update secret with new config file
gcloud secrets versions add merkle-oracle-prod-config \
  --project=palm-portal-staging \
  --data-file=./config.yaml

# Verify the latest version
gcloud secrets versions list merkle-oracle-prod-config --project=palm-portal-staging
```

---

## 2. Restart the Service

### Method A: Restart via VM Reset (Recommended)
This re-runs the startup script which fetches the latest secrets.

```bash
gcloud compute instances reset merkle-oracle-staging \
  --zone=europe-west1-b \
  --project=palm-portal-staging
```

### Method B: SSH and Restart Container Only
```bash
gcloud compute ssh merkle-oracle-staging \
  --zone=europe-west1-b \
  --project=palm-portal-staging \
  --tunnel-through-iap \
  --command="docker restart \$(docker ps -q --filter name=merkle-oracle)"
```

### Method C: Trigger Full Redeployment (via GitHub Actions)
1. Go to **Actions** → **Deploy to GCP Staging**
2. Click **Run workflow**
3. Enter the image tag (e.g., `v1.2.1`)

---

## 3. View Logs

### Option A: Stream Logs Locally (Simplest - One Command)
```bash
# Stream live logs from Cloud Logging
gcloud logging tail "resource.type=gce_instance AND labels.container_name=merkle-oracle-node" \
  --project=palm-portal-staging
```

### Option B: SSH and View Docker Logs
```bash
# SSH with IAP tunnel
gcloud compute ssh merkle-oracle-staging \
  --zone=europe-west1-b \
  --project=palm-portal-staging \
  --tunnel-through-iap

# Once connected:
docker logs -f $(docker ps -q --filter name=merkle-oracle) --tail 100
```

### Option C: Quick One-Liner (No Interactive SSH)
```bash
# Application logs
gcloud compute ssh merkle-oracle-staging \
  --zone=europe-west1-b \
  --project=palm-portal-staging \
  --tunnel-through-iap --quiet \
  --command="docker logs \$(docker ps -q --filter name=merkle-oracle) --tail 100"

# Caddy (proxy) logs
gcloud compute ssh merkle-oracle-staging \
  --zone=europe-west1-b \
  --project=palm-portal-staging \
  --tunnel-through-iap --quiet \
  --command="docker logs merkle-caddy --tail 100"
```

---

## 4. Health Check

```bash
# Quick health check
curl https://merkle-oracle.zengate-dev.com/healthcheck

# Check container status on instance
gcloud compute ssh merkle-oracle-staging \
  --zone=europe-west1-b \
  --project=palm-portal-staging \
  --tunnel-through-iap --quiet \
  --command="docker ps"
```

---

## GitHub Environment Variables (Staging)

| Variable | Value |
|----------|-------|
| `PROJECT_ID` | `palm-portal-staging` |
| `REGION` | `europe-west1` |
| `ZONE` | `europe-west1-b` |
| `INSTANCE_NAME` | `merkle-oracle-staging` |
| `IMAGE_NAME` | `merkle-oracle-node` |
| `DOMAIN` | `merkle-oracle.zengate-dev.com` |
