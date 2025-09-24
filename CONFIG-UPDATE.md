# Update config.yaml in Google Secret Manager (basic)

Use this when you need to change the application config stored in Secret Manager.

- Project: palm-portal-staging
- Secret name: merkle-oracle-config

## Steps (recommended: use Cloud Shell)

1) Set variables

```bash
PROJECT_ID=palm-portal-staging
SECRET_NAME=merkle-oracle-config
CONFIG_FILE=./config.yaml   # path to your new config
```

2) If the secret does not exist (run once)

```bash
gcloud secrets create "$SECRET_NAME" \
  --project="$PROJECT_ID" \
  --replication-policy=automatic
```

3) Add a new version with your config

```bash
gcloud secrets versions add "$SECRET_NAME" \
  --project="$PROJECT_ID" \
  --data-file="$CONFIG_FILE"
```

4) Verify what’s stored (optional)

```bash
gcloud secrets versions access latest \
  --project="$PROJECT_ID" \
  --secret="$SECRET_NAME" \
  | head -n 20
```

Notes
- You need Secret Manager permissions to add versions (roles/secretmanager.secretVersionManager or higher on this secret).
- The VM pulls the latest version automatically during deploy; no GitHub Actions step is required to update the secret.

