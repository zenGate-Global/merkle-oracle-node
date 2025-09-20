#!/bin/bash
set -euo pipefail

# Backup and maintenance script for Merkle Oracle Node
PROJECT_ID="${PROJECT_ID:-palm-portal-staging}"
INSTANCE_NAME="${INSTANCE_NAME:-merkle-oracle-node-staging}"
ZONE="${ZONE:-europe-west1-b}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"

echo "==> Starting backup and maintenance for Merkle Oracle Node"

# Function to run commands on the VM
run_on_vm() {
    local cmd="$1"
    gcloud compute ssh $INSTANCE_NAME \
        --zone=$ZONE \
        --project=$PROJECT_ID \
        --command="$cmd" \
        --ssh-flag="-o StrictHostKeyChecking=no"
}

# Create disk snapshot
create_disk_snapshot() {
    local disk_name="$1"
    local snapshot_name="${disk_name}-$(date +%Y%m%d-%H%M%S)"
    
    echo "Creating snapshot: $snapshot_name"
    gcloud compute disks snapshot $disk_name \
        --zone=$ZONE \
        --snapshot-names=$snapshot_name \
        --description="Automated backup of $disk_name for $INSTANCE_NAME"
    
    echo "Snapshot created: $snapshot_name"
}

# Clean up old snapshots
cleanup_old_snapshots() {
    local disk_name="$1"
    local cutoff_date=$(date -d "$BACKUP_RETENTION_DAYS days ago" +%Y-%m-%d)
    
    echo "Cleaning up snapshots older than $cutoff_date for disk: $disk_name"
    
    gcloud compute snapshots list \
        --filter="sourceDisk:$disk_name AND creationTimestamp<$cutoff_date" \
        --format="value(name)" | while read snapshot; do
        if [ -n "$snapshot" ]; then
            echo "Deleting old snapshot: $snapshot"
            gcloud compute snapshots delete $snapshot --quiet
        fi
    done
}

# Backup database
backup_database() {
    echo "Creating database backup..."
    
    local backup_name="merkle-oracle-backup-$(date +%Y%m%d-%H%M%S)"
    
    # Create Cloud SQL backup
    gcloud sql backups create \
        --instance=merkle-oracle-db \
        --description="Automated backup for $INSTANCE_NAME"
    
    echo "Database backup created"
}

# System maintenance
system_maintenance() {
    echo "Performing system maintenance..."
    
    # Update system packages
    run_on_vm "sudo apt-get update && sudo apt-get upgrade -y"
    
    # Clean up Docker
    run_on_vm "docker system prune -f"
    
    # Rotate logs
    run_on_vm "sudo journalctl --vacuum-time=7d"
    
    # Check disk usage
    echo "Disk usage after cleanup:"
    run_on_vm "df -h"
    
    echo "System maintenance completed"
}

# Health check after maintenance
post_maintenance_health_check() {
    echo "Running post-maintenance health check..."
    
    # Wait for services to stabilize
    sleep 30
    
    # Check service status
    if run_on_vm "sudo systemctl is-active merkle-oracle" | grep -q "active"; then
        echo "✅ Merkle Oracle service is running"
    else
        echo "❌ Merkle Oracle service is not running"
        run_on_vm "sudo systemctl status merkle-oracle --no-pager"
        exit 1
    fi
    
    # Check API health
    EXTERNAL_IP=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(networkInterfaces[0].accessConfigs[0].natIP)')
    
    if curl -f -s --max-time 30 "http://$EXTERNAL_IP:8080/health" > /dev/null 2>&1; then
        echo "✅ API health check passed"
    else
        echo "❌ API health check failed"
        exit 1
    fi
    
    echo "Post-maintenance health check completed successfully"
}

# Main execution
main() {
    case "${1:-all}" in
        "backup")
            echo "==> Running backup only"
            
            # Get disk names
            BOOT_DISK=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(disks[0].deviceName)')
            DATA_DISK=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(disks[1].deviceName)')
            
            # Create snapshots
            create_disk_snapshot $BOOT_DISK
            if [ -n "$DATA_DISK" ]; then
                create_disk_snapshot $DATA_DISK
            fi
            
            # Backup database
            backup_database
            
            # Cleanup old snapshots
            cleanup_old_snapshots $BOOT_DISK
            if [ -n "$DATA_DISK" ]; then
                cleanup_old_snapshots $DATA_DISK
            fi
            ;;
            
        "maintenance")
            echo "==> Running maintenance only"
            system_maintenance
            post_maintenance_health_check
            ;;
            
        "all"|*)
            echo "==> Running full backup and maintenance"
            
            # Get disk names
            BOOT_DISK=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(disks[0].deviceName)')
            DATA_DISK=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(disks[1].deviceName)')
            
            # Create snapshots
            create_disk_snapshot $BOOT_DISK
            if [ -n "$DATA_DISK" ]; then
                create_disk_snapshot $DATA_DISK
            fi
            
            # Backup database
            backup_database
            
            # System maintenance
            system_maintenance
            
            # Cleanup old snapshots
            cleanup_old_snapshots $BOOT_DISK
            if [ -n "$DATA_DISK" ]; then
                cleanup_old_snapshots $DATA_DISK
            fi
            
            # Health check
            post_maintenance_health_check
            ;;
    esac
    
    echo "==> Backup and maintenance completed successfully!"
}

# Run main function with first argument
main "$@"
