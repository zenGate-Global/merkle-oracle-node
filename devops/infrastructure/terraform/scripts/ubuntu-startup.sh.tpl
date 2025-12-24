#!/bin/bash
set -euo pipefail

# ============================================================================
# Ubuntu Startup Script for Containerized Applications
# Template Variables (replaced by Terraform or manually):
#   ${app_name}     - Application name (e.g., merkle-oracle)
#   ${project_id}   - GCP project ID
#   ${region}       - GCP region
#   ${image_tag}    - Docker image tag
#   ${secret_name}  - Secret Manager secret name
#   ${domain}       - Public domain for HTTPS
#   ${app_port}     - Application port (default: 8080)
# ============================================================================

APP_NAME="${app_name}"
PROJECT_ID="${project_id}"
REGION="${region}"
IMAGE_TAG="${image_tag}"
SECRET_NAME="${secret_name}"
DOMAIN="${domain}"
APP_PORT="${app_port}"

# Derived variables
IMAGE="$REGION-docker.pkg.dev/$PROJECT_ID/$APP_NAME/$APP_NAME:$IMAGE_TAG"
DATA_DIR="/opt/$APP_NAME"
CONFIG_DIR="$DATA_DIR/config"
CADDY_DIR="$DATA_DIR/caddy"

# Logging function
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

log "Starting setup for $APP_NAME..."

# ============================================================================
# 1. System Setup
# ============================================================================

log "Updating system packages..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq \
    apt-transport-https \
    ca-certificates \
    curl \
    gnupg \
    lsb-release \
    jq

# ============================================================================
# 2. Install Docker
# ============================================================================

if ! command -v docker &> /dev/null; then
    log "Installing Docker..."
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
    apt-get update -qq
    apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin
    systemctl enable docker
    systemctl start docker
    log "Docker installed successfully"
else
    log "Docker already installed"
fi

# ============================================================================
# 3. Install Google Cloud SDK (for Artifact Registry auth)
# ============================================================================

if ! command -v gcloud &> /dev/null; then
    log "Installing Google Cloud SDK..."
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | tee /etc/apt/sources.list.d/google-cloud-sdk.list
    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg
    apt-get update -qq
    apt-get install -y -qq google-cloud-cli
    log "Google Cloud SDK installed"
fi

# ============================================================================
# 4. Configure Swap (4GB)
# ============================================================================

SWAPFILE="/swapfile"
if [ ! -f "$SWAPFILE" ]; then
    log "Creating 4GB swap file..."
    fallocate -l 4G "$SWAPFILE"
    chmod 600 "$SWAPFILE"
    mkswap "$SWAPFILE"
    swapon "$SWAPFILE"
    echo "$SWAPFILE none swap sw 0 0" >> /etc/fstab
    log "Swap enabled"
else
    swapon "$SWAPFILE" 2>/dev/null || true
fi

# ============================================================================
# 5. Create Directory Structure
# ============================================================================

log "Creating directory structure..."
mkdir -p "$CONFIG_DIR"
mkdir -p "$CADDY_DIR"/{data,config,logs}
mkdir -p "$DATA_DIR/data"
chmod -R 755 "$DATA_DIR"

# ============================================================================
# 6. Authenticate to Artifact Registry
# ============================================================================

log "Authenticating to Artifact Registry..."
gcloud auth configure-docker "$REGION-docker.pkg.dev" --quiet

# ============================================================================
# 7. Fetch Configuration from Secret Manager
# ============================================================================

log "Fetching configuration from Secret Manager..."
ACCESS_TOKEN=$(curl -s -H "Metadata-Flavor: Google" \
    "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token" | jq -r '.access_token')

CONFIG_CONTENT=$(curl -s -H "Authorization: Bearer $ACCESS_TOKEN" \
    "https://secretmanager.googleapis.com/v1/projects/$PROJECT_ID/secrets/$SECRET_NAME/versions/latest:access" | \
    jq -r '.payload.data' | base64 -d)

echo "$CONFIG_CONTENT" > "$CONFIG_DIR/config.yaml"
chmod 644 "$CONFIG_DIR/config.yaml"
log "Configuration written to $CONFIG_DIR/config.yaml"

# ============================================================================
# 8. Create Caddyfile
# ============================================================================

log "Creating Caddyfile..."
cat > "$CADDY_DIR/Caddyfile" << EOF
$DOMAIN {
    # Serve swagger.json as static file
    handle /docs/swagger.json {
        root * /srv
        file_server
    }
    
    # API endpoints - proxy to application
    handle /healthcheck* {
        reverse_proxy localhost:$APP_PORT
    }
    
    handle /objects* {
        reverse_proxy localhost:$APP_PORT
    }
    
    handle /keys* {
        reverse_proxy localhost:$APP_PORT
    }
    
    handle /statistics* {
        reverse_proxy localhost:$APP_PORT
    }
    
    handle /docs* {
        reverse_proxy localhost:$APP_PORT
    }
    
    handle /swagger* {
        reverse_proxy localhost:$APP_PORT
    }
    
    handle / {
        respond "$APP_NAME - Running" 200
    }
    
    # CORS headers
    header {
        Access-Control-Allow-Origin "*"
        Access-Control-Allow-Methods "GET, POST, PUT, DELETE, OPTIONS"
        Access-Control-Allow-Headers "Content-Type, Authorization"
        Access-Control-Max-Age "3600"
    }
    
    # Security headers
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
        X-Frame-Options "DENY"
        X-Content-Type-Options "nosniff"
        X-XSS-Protection "1; mode=block"
        Referrer-Policy "strict-origin-when-cross-origin"
    }
    
    # Compression
    encode zstd gzip
    
    # Logging
    log {
        output file /var/log/caddy/access.log {
            roll_size 100mb
            roll_keep 5
        }
        format json
    }
}
EOF
chmod 644 "$CADDY_DIR/Caddyfile"

# ============================================================================
# 9. Create systemd Service for Application
# ============================================================================

log "Creating systemd service for $APP_NAME..."
cat > "/etc/systemd/system/$APP_NAME.service" << EOF
[Unit]
Description=$APP_NAME Container
Requires=docker.service
After=docker.service

[Service]
Type=simple
Restart=always
RestartSec=10
ExecStartPre=-/usr/bin/docker stop $APP_NAME
ExecStartPre=-/usr/bin/docker rm $APP_NAME
ExecStartPre=/usr/bin/docker pull $IMAGE
ExecStart=/usr/bin/docker run --rm \
    --name $APP_NAME \
    --network host \
    -v $CONFIG_DIR:/etc/config:ro \
    -v $DATA_DIR/data:/data \
    -v $DATA_DIR/docs:/app/docs:ro \
    $IMAGE
ExecStop=/usr/bin/docker stop $APP_NAME

[Install]
WantedBy=multi-user.target
EOF

# ============================================================================
# 10. Create systemd Service for Caddy
# ============================================================================

log "Creating systemd service for Caddy..."
cat > "/etc/systemd/system/$APP_NAME-caddy.service" << EOF
[Unit]
Description=Caddy Reverse Proxy for $APP_NAME
Requires=docker.service
After=docker.service $APP_NAME.service

[Service]
Type=simple
Restart=always
RestartSec=10
ExecStartPre=-/usr/bin/docker stop $APP_NAME-caddy
ExecStartPre=-/usr/bin/docker rm $APP_NAME-caddy
ExecStartPre=/usr/bin/docker pull caddy:2-alpine
ExecStart=/usr/bin/docker run --rm \
    --name $APP_NAME-caddy \
    --network host \
    -v $CADDY_DIR/Caddyfile:/etc/caddy/Caddyfile:ro \
    -v $CADDY_DIR/data:/data \
    -v $CADDY_DIR/config:/config \
    -v $CADDY_DIR/logs:/var/log/caddy \
    -v $DATA_DIR/docs:/srv/docs:ro \
    caddy:2-alpine
ExecStop=/usr/bin/docker stop $APP_NAME-caddy

[Install]
WantedBy=multi-user.target
EOF

# ============================================================================
# 11. Extract swagger.json from Docker Image
# ============================================================================

log "Extracting swagger.json from Docker image..."
mkdir -p "$DATA_DIR/docs"

# Pull the image if not already present
docker pull "$IMAGE" > /dev/null 2>&1 || true

# Extract swagger.json from the Docker image
TEMP_CONTAINER=$(docker create "$IMAGE")
docker cp "$TEMP_CONTAINER:/app/docs/swagger.json" "$DATA_DIR/docs/swagger.json" 2>/dev/null || \
    log "Warning: swagger.json not found in Docker image at /app/docs/swagger.json"
docker rm "$TEMP_CONTAINER" > /dev/null 2>&1

if [ -f "$DATA_DIR/docs/swagger.json" ]; then
    chmod 644 "$DATA_DIR/docs/swagger.json"
    log "swagger.json extracted successfully"
else
    log "Warning: swagger.json could not be extracted, creating placeholder"
    echo '{"swagger":"2.0","info":{"title":"API Documentation","version":"1.0.0"},"paths":{}}' > "$DATA_DIR/docs/swagger.json"
fi

# ============================================================================
# 12. Enable and Start Services
# ============================================================================

log "Enabling and starting services..."
systemctl daemon-reload
systemctl enable "$APP_NAME.service"
systemctl enable "$APP_NAME-caddy.service"
systemctl start "$APP_NAME.service"

# Wait for app to be ready before starting Caddy
log "Waiting for application to be ready..."
for i in {1..30}; do
    if curl -sf "http://localhost:$APP_PORT/healthcheck" > /dev/null 2>&1; then
        log "Application is ready"
        break
    fi
    if [ $i -eq 30 ]; then
        log "Warning: Application not responding after 150 seconds, starting Caddy anyway"
    fi
    sleep 5
done

systemctl start "$APP_NAME-caddy.service"

# ============================================================================
# 12. Final Health Check
# ============================================================================

log "Performing final health check..."
sleep 10

if systemctl is-active --quiet "$APP_NAME.service"; then
    log "✓ $APP_NAME service is running"
else
    log "✗ $APP_NAME service failed to start"
    journalctl -u "$APP_NAME.service" --no-pager -n 50
fi

if systemctl is-active --quiet "$APP_NAME-caddy.service"; then
    log "✓ Caddy service is running"
else
    log "✗ Caddy service failed to start"
    journalctl -u "$APP_NAME-caddy.service" --no-pager -n 50
fi

log "Startup script completed for $APP_NAME"
