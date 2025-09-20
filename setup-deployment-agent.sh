#!/bin/bash

# Setup script for Merkle Oracle Zero-Downtime Deployment Agent
# This script installs and configures the deployment agent on the VM

set -euo pipefail

echo "Setting up Merkle Oracle Zero-Downtime Deployment Agent..."

# Copy deployment agent script
echo "Installing deployment agent script..."
sudo cp deployment-agent.sh /usr/local/bin/merkle-oracle-deploy-agent.sh
sudo chmod +x /usr/local/bin/merkle-oracle-deploy-agent.sh

# Copy systemd service file
echo "Installing systemd service..."
sudo cp merkle-oracle-deploy-agent.service /etc/systemd/system/

# Create log directory
sudo mkdir -p /var/log
sudo touch /var/log/merkle-oracle-deploy.log
sudo chmod 644 /var/log/merkle-oracle-deploy.log

# Reload systemd and enable service
echo "Enabling deployment agent service..."
sudo systemctl daemon-reload
sudo systemctl enable merkle-oracle-deploy-agent.service

# Start the service
echo "Starting deployment agent..."
sudo systemctl start merkle-oracle-deploy-agent.service

# Check service status
echo "Checking service status..."
sudo systemctl status merkle-oracle-deploy-agent.service --no-pager

echo "✅ Deployment agent setup complete!"
echo ""
echo "The agent will now monitor for deployment requests every 30 seconds."
echo "To check logs: sudo journalctl -u merkle-oracle-deploy-agent.service -f"
echo "To check deployment log: sudo tail -f /var/log/merkle-oracle-deploy.log"
