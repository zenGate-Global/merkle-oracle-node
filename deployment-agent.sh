#!/bin/bash

# Merkle Oracle Zero-Downtime Deployment Agent
# This script runs as a systemd service and monitors for deployment requests
# It performs rolling updates without service interruption

set -euo pipefail

# Configuration
METADATA_URL="http://metadata.google.internal/computeMetadata/v1/instance/attributes"
DOCKER_REGISTRY="europe-west1-docker.pkg.dev"
SERVICE_NAME="merkle-oracle-node"
HEALTH_CHECK_URL="http://localhost:8080/healthcheck"
HEALTH_CHECK_TIMEOUT=30
POLL_INTERVAL=30
LOG_PREFIX="[DEPLOY-AGENT]"

# Logging function
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $LOG_PREFIX $1" | tee -a /var/log/merkle-oracle-deploy.log
}

# Get metadata value
get_metadata() {
    local key="$1"
    curl -s -f -H "Metadata-Flavor: Google" "$METADATA_URL/$key" 2>/dev/null || echo ""
}

# Remove metadata key
remove_metadata() {
    local key="$1"
    local instance_name=$(curl -s -H "Metadata-Flavor: Google" "http://metadata.google.internal/computeMetadata/v1/instance/name")
    local zone=$(curl -s -H "Metadata-Flavor: Google" "http://metadata.google.internal/computeMetadata/v1/instance/zone" | cut -d'/' -f4)
    
    gcloud compute instances remove-metadata "$instance_name" \
        --zone="$zone" \
        --keys="$key" 2>/dev/null || true
}

# Authenticate Docker
authenticate_docker() {
    log "Authenticating Docker with Artifact Registry"
    local access_token=$(curl -s -H 'Metadata-Flavor: Google' 'http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token' | cut -d'"' -f4)
    echo "$access_token" | docker login -u oauth2accesstoken --password-stdin "https://$DOCKER_REGISTRY" >/dev/null 2>&1
}

# Health check function
health_check() {
    local port="$1"
    local url="http://localhost:$port/healthcheck"
    
    log "Performing health check on port $port"
    for i in $(seq 1 $HEALTH_CHECK_TIMEOUT); do
        if curl -s -f "$url" >/dev/null 2>&1; then
            log "Health check passed on port $port"
            return 0
        fi
        sleep 1
    done
    
    log "Health check failed on port $port after $HEALTH_CHECK_TIMEOUT seconds"
    return 1
}

# Get current running image
get_current_image() {
    docker ps --filter "name=merkle-oracle-main" --format "{{.Image}}" 2>/dev/null || echo ""
}

# Perform zero-downtime deployment
deploy_image() {
    local new_image="$1"
    local current_image=$(get_current_image)
    
    log "Starting zero-downtime deployment"
    log "Current image: $current_image"
    log "New image: $new_image"
    
    if [ "$current_image" = "$new_image" ]; then
        log "Image is already deployed, skipping deployment"
        return 0
    fi
    
    # Authenticate Docker
    authenticate_docker
    
    # Pull new image
    log "Pulling new image: $new_image"
    if ! docker pull "$new_image"; then
        log "ERROR: Failed to pull image $new_image"
        return 1
    fi
    
    # Update config from Secret Manager
    log "Updating configuration from Secret Manager"
    systemctl start merkle-oracle-startup || true
    
    # Start new container on port 8081 (staging)
    log "Starting new container on staging port 8081"
    docker stop merkle-oracle-staging 2>/dev/null || true
    docker rm merkle-oracle-staging 2>/dev/null || true
    
    # Get current image tag for the service
    echo "$new_image" > /tmp/merkle-oracle-current-image
    
    # Start staging container
    docker run -d \
        --name merkle-oracle-staging \
        --restart unless-stopped \
        -p 127.0.0.1:8081:8080 \
        -v /tmp/merkle-oracle-config:/data/config:ro \
        "$new_image" \
        /bin/node --config-file=/data/config/config.yaml
    
    # Wait for container to start
    sleep 5
    
    # Health check new container
    if ! health_check 8081; then
        log "ERROR: New container failed health check, rolling back"
        docker stop merkle-oracle-staging 2>/dev/null || true
        docker rm merkle-oracle-staging 2>/dev/null || true
        return 1
    fi
    
    # Switch traffic to new container
    log "Switching traffic to new container"
    
    # Stop old main container gracefully
    if docker ps --filter "name=merkle-oracle-main" --quiet | grep -q .; then
        log "Stopping old container gracefully"
        docker stop merkle-oracle-main --time=10 2>/dev/null || true
        docker rm merkle-oracle-main 2>/dev/null || true
    fi
    
    # Rename staging container to main and update port mapping
    docker stop merkle-oracle-staging
    docker commit merkle-oracle-staging merkle-oracle-temp
    docker rm merkle-oracle-staging
    
    # Start new main container
    docker run -d \
        --name merkle-oracle-main \
        --restart unless-stopped \
        -p 127.0.0.1:8080:8080 \
        -v /tmp/merkle-oracle-config:/data/config:ro \
        merkle-oracle-temp \
        /bin/node --config-file=/data/config/config.yaml
    
    # Clean up temp image
    docker rmi merkle-oracle-temp 2>/dev/null || true
    
    # Final health check
    sleep 3
    if ! health_check 8080; then
        log "ERROR: Final health check failed after deployment"
        return 1
    fi
    
    log "Zero-downtime deployment completed successfully"
    
    # Clean up old images (keep last 2)
    log "Cleaning up old Docker images"
    docker images --filter "reference=europe-west1-docker.pkg.dev/palm-portal-staging/merkle-oracle-node/merkle-oracle-node" --format "{{.ID}}" | tail -n +3 | xargs -r docker rmi 2>/dev/null || true
    
    return 0
}

# Main deployment agent loop
main() {
    log "Merkle Oracle Deployment Agent started"
    log "Polling interval: ${POLL_INTERVAL}s"
    
    while true; do
        # Check for deployment request
        local deploy_image=$(get_metadata "deploy-image")
        local deploy_timestamp=$(get_metadata "deploy-timestamp")
        
        if [ -n "$deploy_image" ] && [ -n "$deploy_timestamp" ]; then
            log "Deployment request detected: $deploy_image (timestamp: $deploy_timestamp)"
            
            # Perform deployment
            if deploy_image "$deploy_image"; then
                log "Deployment successful, cleaning up metadata"
                remove_metadata "deploy-image"
                remove_metadata "deploy-timestamp"
            else
                log "Deployment failed, keeping metadata for retry"
            fi
        fi
        
        # Wait before next poll
        sleep $POLL_INTERVAL
    done
}

# Handle signals gracefully
trap 'log "Deployment agent shutting down"; exit 0' SIGTERM SIGINT

# Start main loop
main
