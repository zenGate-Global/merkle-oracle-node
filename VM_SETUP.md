# Production Ubuntu VM Setup for Merkle Oracle Node

This document contains the exact commands used to set up the production Ubuntu VM for the Merkle Oracle Node deployment with automatic startup, restart capabilities, and secure configuration management.

## 🏗️ Infrastructure Overview

- **VM Instance**: `merkle-oracle-ubuntu` (e2-medium, Ubuntu 22.04 LTS)
- **Database**: Cloud SQL PostgreSQL (`palm-portal-staging-db`)
- **Container Registry**: Google Artifact Registry
- **Configuration**: Google Secret Manager (secure, versioned)
- **HTTPS Access**: Cloudflare Tunnel (free, professional URLs)
- **Service Management**: systemd services for production reliability

## Prerequisites

- Google Cloud SDK installed and authenticated
- Access to the `palm-portal-staging` project
- Artifact Registry repository with the application image
- Secret Manager API enabled
- Proper IAM permissions for VM service account

## VM Creation

```bash
gcloud compute instances create merkle-oracle-ubuntu \
    --zone=europe-west1-b \
    --machine-type=e2-medium \
    --image-family=ubuntu-2204-lts \
    --image-project=ubuntu-os-cloud \
    --boot-disk-size=20GB \
    --boot-disk-type=pd-standard \
    --tags=merkle-oracle-http,merkle-oracle-ssh
```

## System Setup

### Update System Packages
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo apt-get update"
```

### Install Dependencies
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release"
```

## Docker Installation

### Add Docker GPG Key
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg"
```

### Add Docker Repository
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu jammy stable' | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null"
```

### Update Package Index
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo apt-get update"
```

### Install Docker
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo apt-get install -y docker-ce docker-ce-cli containerd.io"
```

### Configure Docker Service
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo systemctl enable docker"
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo systemctl start docker"
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo usermod -aG docker \$USER"
```

### Install Additional Tools
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo apt-get install -y curl wget htop netcat-openbsd jq"
```

## Application Configuration

### Transfer Configuration from Old VM
```bash
gcloud compute ssh merkle-oracle-node-staging --zone=europe-west1-b --command="cat /tmp/merkle-oracle-config/config.yaml" > /tmp/config-backup.yaml
gcloud compute scp /tmp/config-backup.yaml merkle-oracle-ubuntu:/tmp/config.yaml --zone=europe-west1-b
```

### Setup Configuration Directory
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="mkdir -p /tmp/merkle-oracle-config"
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="cp /tmp/config.yaml /tmp/merkle-oracle-config/config.yaml"
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="chmod 644 /tmp/merkle-oracle-config/config.yaml"
```

## Container Deployment

### Configure Docker Registry Authentication
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="echo 'Y' | gcloud auth configure-docker europe-west1-docker.pkg.dev"
```

### Deploy Application Container (HTTPS-Only)
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="docker run -d --name merkle-oracle-node --restart unless-stopped -p 127.0.0.1:8080:8080 -p 9094:9094 -p 9093:9093 -v /tmp/merkle-oracle-config/config.yaml:/etc/config/config.yaml:ro --log-driver=gcplogs europe-west1-docker.pkg.dev/palm-portal-staging/merkle-oracle-node/merkle-oracle-node:test-1758188519"
```

**Note**: HTTP port (8080) is bound to localhost only for security. External access is HTTPS-only via ngrok.

## 🔧 Production Services Setup

### systemd Services for Production Reliability

```bash
# Create startup preparation service
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="
sudo tee /etc/systemd/system/merkle-oracle-startup.service > /dev/null << 'EOF'
[Unit]
Description=Merkle Oracle Startup Preparation
Requires=network-online.target
After=network-online.target
Before=merkle-oracle-node.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/merkle-oracle-startup.sh
RemainAfterExit=yes
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# Create startup script
sudo tee /usr/local/bin/merkle-oracle-startup.sh > /dev/null << 'EOF'
#!/bin/bash
set -euo pipefail

# Authenticate Docker with Artifact Registry
ACCESS_TOKEN=\$(curl -s -H 'Metadata-Flavor: Google' 'http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token' | cut -d'\"' -f4)
echo \$ACCESS_TOKEN | docker login -u oauth2accesstoken --password-stdin https://europe-west1-docker.pkg.dev

# Fetch config from Secret Manager
mkdir -p /tmp/merkle-oracle-config
gcloud secrets versions access latest --secret='merkle-oracle-config' > /tmp/merkle-oracle-config/config.yaml
chmod 644 /tmp/merkle-oracle-config/config.yaml

echo 'Merkle Oracle startup preparation completed'
EOF

sudo chmod +x /usr/local/bin/merkle-oracle-startup.sh

# Create main application service
sudo tee /etc/systemd/system/merkle-oracle-node.service > /dev/null << 'EOF'
[Unit]
Description=Merkle Oracle Node Docker Container
Requires=docker.service merkle-oracle-startup.service
After=docker.service merkle-oracle-startup.service
StartLimitIntervalSec=0

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=-/usr/bin/docker stop merkle-oracle-node
ExecStartPre=-/usr/bin/docker rm merkle-oracle-node
ExecStart=/usr/local/bin/merkle-oracle-run.sh
ExecStop=/usr/bin/docker stop merkle-oracle-node
ExecStopPost=/usr/bin/docker rm merkle-oracle-node
Restart=on-failure
RestartSec=30

[Install]
WantedBy=multi-user.target
EOF

# Create container run script
sudo tee /usr/local/bin/merkle-oracle-run.sh > /dev/null << 'EOF'
#!/bin/bash
set -euo pipefail

# Default image tag (update this with your current image)
DEFAULT_IMAGE=\"europe-west1-docker.pkg.dev/palm-portal-staging/merkle-oracle-node/merkle-oracle-node:test-1758188519\"

# Check if there's a current image file from deployment
if [ -f /tmp/merkle-oracle-current-image ]; then
    IMAGE_TAG=\$(cat /tmp/merkle-oracle-current-image)
    echo \"Using deployment image: \$IMAGE_TAG\"
else
    IMAGE_TAG=\"\$DEFAULT_IMAGE\"
    echo \"Using default image: \$IMAGE_TAG\"
fi

# Pull the image
docker pull \"\$IMAGE_TAG\"

# Run the container
docker run -d \
  --name merkle-oracle-node \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -p 9094:9094 \
  -p 9093:9093 \
  -v /tmp/merkle-oracle-config/config.yaml:/etc/config/config.yaml:ro \
  --log-driver=gcplogs \
  \"\$IMAGE_TAG\"

echo \"Merkle Oracle Node started with image: \$IMAGE_TAG\"
EOF

sudo chmod +x /usr/local/bin/merkle-oracle-run.sh

# Enable all services
sudo systemctl enable docker
sudo systemctl enable merkle-oracle-startup
sudo systemctl enable merkle-oracle-node

# Reload systemd
sudo systemctl daemon-reload
"
```

### Service Management Commands

```bash
# Start services
sudo systemctl start merkle-oracle-startup
sudo systemctl start merkle-oracle-node

# Check service status
sudo systemctl status merkle-oracle-startup
sudo systemctl status merkle-oracle-node

# View service logs
sudo journalctl -u merkle-oracle-startup -f
sudo journalctl -u merkle-oracle-node -f

# Restart services
sudo systemctl restart merkle-oracle-node
```

## HTTPS Setup with Cloudflare Tunnel (Free)

### Setup Cloudflare Tunnel Service
```bash
# Create systemd service for Cloudflare tunnel
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="
sudo tee /etc/systemd/system/cloudflare-tunnel.service > /dev/null <<'EOF'
[Unit]
Description=Cloudflare Tunnel
After=network.target

[Service]
Type=simple
User=nobody
ExecStart=/usr/local/bin/cloudflared tunnel --url http://localhost:8080
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF"
```

### Enable and Start Service
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo systemctl daemon-reload && sudo systemctl enable cloudflare-tunnel && sudo systemctl start cloudflare-tunnel"
```

### Get HTTPS URL
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="sudo journalctl -u cloudflare-tunnel --no-pager -l | grep -o 'https://.*\.trycloudflare\.com' | tail -1"
```

## Verification

### Check Container Status
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="docker ps"
```

### Check Application Logs
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b --command="docker logs merkle-oracle-node --tail 10"
```

### Get VM IP Address
```bash
gcloud compute instances describe merkle-oracle-ubuntu --zone=europe-west1-b --format="value(networkInterfaces[0].accessConfigs[0].natIP)"
```

## Cleanup

### Remove Temporary Files
```bash
rm -f /tmp/config-backup.yaml
```

## VM Information

- **Name**: merkle-oracle-ubuntu
- **Zone**: europe-west1-b
- **Machine Type**: e2-medium
- **OS**: Ubuntu 22.04 LTS
- **Ports**: 8080 (API), 9094 (Metrics), 9093 (Additional)

## Access

### SSH Access
```bash
gcloud compute ssh merkle-oracle-ubuntu --zone=europe-west1-b
```

### API Endpoints
- HTTP: **DISABLED** (for security)
- HTTPS: `https://CLOUDFLARE_URL` (from Cloudflare tunnel)
- Internal: `http://localhost:8080` (VM localhost only)
- Health Check: `/healthcheck`
- Documentation: `/docs`
