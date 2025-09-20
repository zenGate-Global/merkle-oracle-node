#!/bin/bash
set -euo pipefail

# Health check script for Merkle Oracle Node
# This script checks the health of the application and its dependencies

PROJECT_ID="${PROJECT_ID:-palm-portal-staging}"
ZONE="${ZONE:-europe-west1-b}"
INSTANCE_NAME="${INSTANCE_NAME:-merkle-oracle-node}"

echo "==> Health Check for Merkle Oracle Node"
echo "Project: $PROJECT_ID"
echo "Zone: $ZONE"
echo "Instance: $INSTANCE_NAME"

# Function to run commands on the VM
run_on_vm() {
    local cmd="$1"
    gcloud compute ssh $INSTANCE_NAME \
        --zone=$ZONE \
        --project=$PROJECT_ID \
        --command="$cmd" \
        --ssh-flag="-o StrictHostKeyChecking=no" 2>/dev/null
}

# Get external IP
EXTERNAL_IP=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(networkInterfaces[0].accessConfigs[0].natIP)')

echo "==> Checking VM instance status"
INSTANCE_STATUS=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(status)')
echo "Instance Status: $INSTANCE_STATUS"

if [ "$INSTANCE_STATUS" != "RUNNING" ]; then
    echo "❌ Instance is not running"
    exit 1
fi

echo "==> Checking systemd services"

# Check Cloud SQL Proxy
echo "Checking Cloud SQL Proxy..."
if run_on_vm "sudo systemctl is-active cloud-sql-proxy" | grep -q "active"; then
    echo "✅ Cloud SQL Proxy is running"
else
    echo "❌ Cloud SQL Proxy is not running"
    run_on_vm "sudo systemctl status cloud-sql-proxy --no-pager"
fi

# Check Merkle Oracle service
echo "Checking Merkle Oracle service..."
if run_on_vm "sudo systemctl is-active merkle-oracle" | grep -q "active"; then
    echo "✅ Merkle Oracle service is running"
else
    echo "❌ Merkle Oracle service is not running"
    run_on_vm "sudo systemctl status merkle-oracle --no-pager"
fi

echo "==> Checking Docker container"
CONTAINER_STATUS=$(run_on_vm "docker ps --filter name=merkle-oracle-node --format '{{.Status}}'" || echo "not found")
echo "Container Status: $CONTAINER_STATUS"

if echo "$CONTAINER_STATUS" | grep -q "Up"; then
    echo "✅ Docker container is running"
else
    echo "❌ Docker container is not running"
    run_on_vm "docker ps -a --filter name=merkle-oracle-node"
fi

echo "==> Checking application endpoints"

# Check API health endpoint
echo "Checking API health endpoint..."
if curl -f -s --max-time 10 "http://$EXTERNAL_IP:8080/health" > /dev/null 2>&1; then
    echo "✅ API health endpoint is responding"
    API_RESPONSE=$(curl -s "http://$EXTERNAL_IP:8080/health" | head -c 200)
    echo "Response: $API_RESPONSE"
else
    echo "❌ API health endpoint is not responding"
fi

# Check metrics endpoint
echo "Checking metrics endpoint..."
if curl -f -s --max-time 10 "http://$EXTERNAL_IP:9094/metrics" > /dev/null 2>&1; then
    echo "✅ Metrics endpoint is responding"
    METRICS_COUNT=$(curl -s "http://$EXTERNAL_IP:9094/metrics" | grep -c "^merkle_oracle" || echo "0")
    echo "Metrics available: $METRICS_COUNT"
else
    echo "❌ Metrics endpoint is not responding"
fi

echo "==> Checking database connectivity"
DB_CHECK=$(run_on_vm "docker exec merkle-oracle-node sh -c 'echo \"SELECT 1;\" | timeout 5 psql \$DATABASE_URL -t'" 2>/dev/null || echo "failed")
if echo "$DB_CHECK" | grep -q "1"; then
    echo "✅ Database connection is working"
else
    echo "❌ Database connection failed"
fi

echo "==> Checking disk usage"
DISK_USAGE=$(run_on_vm "df -h /opt/merkle-oracle/data | tail -1 | awk '{print \$5}'" || echo "unknown")
echo "Data disk usage: $DISK_USAGE"

echo "==> Checking recent logs"
echo "Recent application logs:"
run_on_vm "sudo journalctl -u merkle-oracle --no-pager -n 5" || echo "Could not fetch logs"

echo "==> Health check completed"
echo "Application URL: http://$EXTERNAL_IP:8080"
echo "Metrics URL: http://$EXTERNAL_IP:9094/metrics"
