# 🚀 Deployment Commands

## Quick Deploy

### Option 1: Push to Branch (Automatic) ⭐ Recommended for Testing

```bash
# Create and push to deploy-gcloud-v2 branch
git checkout -b deploy-gcloud-v2
git add .
git commit -m "Deploy to staging"
git push origin deploy-gcloud-v2
```

**This will automatically:**
- Trigger the deployment workflow
- Use the Git commit SHA as the Docker tag
- Build, push, and deploy to staging

### Option 2: Manual Trigger with Custom Tag

```bash
# Deploy with a specific tag
gh workflow run deploy-gcp.yml -f tag=v1.0.0

# Deploy with current SHA (same as push)
gh workflow run deploy-gcp.yml
```

---

## 📋 Step-by-Step First Deployment

### 1. Add GitHub Secrets (One-Time)

Go to: https://github.com/zenGate-Global/merkle-oracle-node/settings/secrets/actions

Add:
- **`WIF_PROVIDER`**: `projects/995274127540/locations/global/workloadIdentityPools/github-actions-pool/providers/github-provider`
- **`WIF_SERVICE_ACCOUNT`**: `github-actions-deployer@palm-portal-staging.iam.gserviceaccount.com`

### 2. Create GitHub Environment (One-Time)

Go to: https://github.com/zenGate-Global/merkle-oracle-node/settings/environments

Create environment: `staging`

### 3. Deploy!

```bash
# Make sure you're on the latest code
git checkout main
git pull origin main

# Create deploy branch
git checkout -b deploy-gcloud-v2

# Push to trigger deployment
git push origin deploy-gcloud-v2
```

---

## 📊 Monitor Deployment

```bash
# Watch the deployment in real-time
gh run watch

# List recent deployments
gh run list --workflow=deploy-gcp.yml --limit 5

# View specific deployment logs
gh run view <run-id> --log

# Or view in browser
gh run view <run-id> --web
```

---

## ✅ Verify Deployment

```bash
# Check health
curl https://merkle-oracle.zengate-dev.com/healthcheck

# Test API
curl https://merkle-oracle.zengate-dev.com/objects?limit=5

# Check containers on instance
gcloud compute ssh merkle-oracle-staging --zone=europe-west1-b --command="docker ps"

# View application logs
gcloud compute ssh merkle-oracle-staging --zone=europe-west1-b --command="docker logs merkle-oracle-node --tail 50"
```

---

## 🔄 Subsequent Deployments

After the first deployment, you can deploy in two ways:

### Method 1: Push to Branch
```bash
# Make your changes
git add .
git commit -m "Your changes"

# Push to deploy branch
git checkout deploy-gcloud-v2
git merge main  # or cherry-pick specific commits
git push origin deploy-gcloud-v2
```

### Method 2: Manual Trigger
```bash
# From any branch, trigger with a tag
gh workflow run deploy-gcp.yml -f tag=v1.0.1

# Or without a tag (uses current SHA)
gh workflow run deploy-gcp.yml
```

---

## 🎯 Complete Example

```bash
# 1. Start from main
git checkout main
git pull origin main

# 2. Make your changes
# ... edit files ...

# 3. Commit changes
git add .
git commit -m "feat: add new feature"

# 4. Create/update deploy branch
git checkout -b deploy-gcloud-v2  # or: git checkout deploy-gcloud-v2
git push origin deploy-gcloud-v2

# 5. Watch deployment
gh run watch

# 6. Verify
curl https://merkle-oracle.zengate-dev.com/healthcheck

# 7. If successful, merge to main
git checkout main
git merge deploy-gcloud-v2
git push origin main
```

---

## 🔧 Troubleshooting

### Deployment doesn't start
- Check GitHub secrets are added
- Check `staging` environment exists
- Check you pushed to `deploy-gcloud-v2` branch

### Deployment fails
```bash
# View logs
gh run list --workflow=deploy-gcp.yml --limit 1
gh run view <run-id> --log

# Check instance
gcloud compute ssh merkle-oracle-staging --zone=europe-west1-b
docker ps -a
docker logs merkle-oracle-node --tail 100
```

### Container not starting
```bash
# SSH to instance
gcloud compute ssh merkle-oracle-staging --zone=europe-west1-b

# Check logs
docker logs merkle-oracle-node --tail 100

# Check config
cat /mnt/stateful_partition/config/config.yaml
```

---

## 📝 Notes

- **Docker tag**: Uses Git SHA by default (e.g., `abc123def456`)
- **Branch**: `deploy-gcloud-v2` triggers automatic deployment
- **Manual trigger**: Can specify custom tag or use SHA
- **Environment**: Always deploys to `staging`
- **Domain**: `merkle-oracle.zengate-dev.com`

---

## 🎉 Quick Reference

```bash
# Deploy (automatic)
git push origin deploy-gcloud-v2

# Deploy (manual)
gh workflow run deploy-gcp.yml

# Monitor
gh run watch

# Verify
curl https://merkle-oracle.zengate-dev.com/healthcheck

# Debug
gcloud compute ssh merkle-oracle-staging --zone=europe-west1-b
```
