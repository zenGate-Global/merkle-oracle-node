#!/bin/bash

# Startup script for merkle-oracle-node COS instance
# This script:
# 1. Creates required directories
# 2. Fetches config from Secret Manager
# 3. Manages Caddy container

# Create required directories
mkdir -p /mnt/stateful_partition/config
mkdir -p /mnt/stateful_partition/caddy/data
mkdir -p /mnt/stateful_partition/caddy/config
mkdir -p /mnt/stateful_partition/caddy/logs


SWAPFILE="/mnt/stateful_partition/swapfile"
if [ ! -f $SWAPFILE ]; then
    echo "Creating 4GB swap file..."
    fallocate -l 4G $SWAPFILE
    chmod 600 $SWAPFILE
    mkswap $SWAPFILE
fi
swapon $SWAPFILE
echo "Swap enabled."


# Fetch config from Secret Manager using Python
python3 << 'PYTHON_SCRIPT'
import urllib.request
import json
import base64

# Get access token from metadata server
req = urllib.request.Request(
    "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
    headers={"Metadata-Flavor": "Google"}
)
token_response = urllib.request.urlopen(req).read()
token = json.loads(token_response)["access_token"]

# Fetch secret from Secret Manager
req = urllib.request.Request(
    "https://secretmanager.googleapis.com/v1/projects/palm-portal-staging/secrets/merkle-oracle-prod-config/versions/latest:access",
    headers={"Authorization": f"Bearer {token}"}
)
secret_response = urllib.request.urlopen(req).read()
secret_data = json.loads(secret_response)["payload"]["data"]

# Decode base64 and write to file
config_content = base64.b64decode(secret_data).decode('utf-8')
with open("/mnt/stateful_partition/config/config.yaml", "w") as f:
    f.write(config_content)
PYTHON_SCRIPT

# Set permissions
chmod -R 755 /mnt/stateful_partition/config
chmod -R 755 /mnt/stateful_partition/caddy

# Create Caddyfile if it doesn't exist
if [ ! -f /mnt/stateful_partition/caddy/Caddyfile ]; then
  cat > /mnt/stateful_partition/caddy/Caddyfile << 'EOF'
merkle-oracle.zengate-dev.com {
    # API endpoints
    handle /healthcheck* {
        reverse_proxy localhost:8080
    }
    
    handle /objects* {
        reverse_proxy localhost:8080
    }
    
    handle /keys* {
        reverse_proxy localhost:8080
    }
    
    handle /statistics* {
        reverse_proxy localhost:8080
    }
    
    handle /docs* {
        reverse_proxy localhost:8080
    }
    
    handle /swagger* {
        reverse_proxy localhost:8080
    }
    
    handle / {
        respond "Merkle Oracle Node - Staging" 200
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
  chmod 644 /mnt/stateful_partition/caddy/Caddyfile
fi

# Start Caddy container if not running
if ! docker ps | grep -q merkle-caddy; then
  docker run -d \
    --name merkle-caddy \
    --restart unless-stopped \
    --network host \
    --log-driver=gcplogs \
    --log-opt gcp-log-cmd=true \
    --log-opt labels=container_name \
    --log-opt env=ENVIRONMENT \
    -v /mnt/stateful_partition/caddy/Caddyfile:/etc/caddy/Caddyfile \
    -v /mnt/stateful_partition/caddy/data:/data \
    -v /mnt/stateful_partition/caddy/config:/config \
    -v /mnt/stateful_partition/caddy/logs:/var/log/caddy \
    caddy:2-alpine
fi

