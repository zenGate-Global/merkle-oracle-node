#!/bin/bash
set -euo pipefail

# Setup monitoring and alerting for Merkle Oracle Node
PROJECT_ID="${PROJECT_ID:-palm-portal-staging}"
INSTANCE_NAME="${INSTANCE_NAME:-merkle-oracle-node-staging}"
ZONE="${ZONE:-europe-west1-b}"

echo "==> Setting up monitoring for Merkle Oracle Node"

# Create uptime check
gcloud monitoring uptime create \
    --display-name="Merkle Oracle API Health Check" \
    --http-check-path="/health" \
    --http-check-port=8080 \
    --monitored-resource-type="gce_instance" \
    --monitored-resource-labels="instance_id=$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(id)'),zone=$ZONE,project_id=$PROJECT_ID" \
    --period=60s \
    --timeout=10s

# Create notification channel (replace with your email)
NOTIFICATION_CHANNEL=$(gcloud alpha monitoring channels create \
    --display-name="Email Notifications" \
    --type=email \
    --channel-labels=email_address=your-email@example.com \
    --format="value(name)")

# Create alerting policy for service down
cat > /tmp/service-down-policy.yaml << EOF
displayName: "Merkle Oracle Service Down"
documentation:
  content: "The Merkle Oracle Node service is down or not responding to health checks."
conditions:
  - displayName: "Service Health Check Failed"
    conditionThreshold:
      filter: 'resource.type="uptime_url"'
      comparison: COMPARISON_EQUAL
      thresholdValue: 1
      duration: 300s
      aggregations:
        - alignmentPeriod: 60s
          perSeriesAligner: ALIGN_RATE
          crossSeriesReducer: REDUCE_COUNT_FALSE
          groupByFields:
            - resource.label.project_id
notificationChannels:
  - $NOTIFICATION_CHANNEL
alertStrategy:
  autoClose: 86400s
EOF

gcloud alpha monitoring policies create --policy-from-file=/tmp/service-down-policy.yaml

# Create alerting policy for high CPU usage
cat > /tmp/high-cpu-policy.yaml << EOF
displayName: "Merkle Oracle High CPU Usage"
documentation:
  content: "The Merkle Oracle Node is experiencing high CPU usage."
conditions:
  - displayName: "High CPU Usage"
    conditionThreshold:
      filter: 'resource.type="gce_instance" AND resource.label.instance_id="$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(id)')" AND metric.type="compute.googleapis.com/instance/cpu/utilization"'
      comparison: COMPARISON_GREATER_THAN
      thresholdValue: 0.8
      duration: 300s
      aggregations:
        - alignmentPeriod: 300s
          perSeriesAligner: ALIGN_MEAN
notificationChannels:
  - $NOTIFICATION_CHANNEL
alertStrategy:
  autoClose: 86400s
EOF

gcloud alpha monitoring policies create --policy-from-file=/tmp/high-cpu-policy.yaml

# Create alerting policy for high memory usage
cat > /tmp/high-memory-policy.yaml << EOF
displayName: "Merkle Oracle High Memory Usage"
documentation:
  content: "The Merkle Oracle Node is experiencing high memory usage."
conditions:
  - displayName: "High Memory Usage"
    conditionThreshold:
      filter: 'resource.type="gce_instance" AND resource.label.instance_id="$(gcloud compute instances describe $INSTANCE_NAME --zone=$ZONE --format='get(id)')" AND metric.type="agent.googleapis.com/memory/percent_used"'
      comparison: COMPARISON_GREATER_THAN
      thresholdValue: 85
      duration: 300s
      aggregations:
        - alignmentPeriod: 300s
          perSeriesAligner: ALIGN_MEAN
notificationChannels:
  - $NOTIFICATION_CHANNEL
alertStrategy:
  autoClose: 86400s
EOF

gcloud alpha monitoring policies create --policy-from-file=/tmp/high-memory-policy.yaml

# Create dashboard
cat > /tmp/dashboard.json << 'EOF'
{
  "displayName": "Merkle Oracle Node Dashboard",
  "mosaicLayout": {
    "tiles": [
      {
        "width": 6,
        "height": 4,
        "widget": {
          "title": "CPU Utilization",
          "xyChart": {
            "dataSets": [
              {
                "timeSeriesQuery": {
                  "timeSeriesFilter": {
                    "filter": "resource.type=\"gce_instance\" AND metric.type=\"compute.googleapis.com/instance/cpu/utilization\"",
                    "aggregation": {
                      "alignmentPeriod": "60s",
                      "perSeriesAligner": "ALIGN_MEAN"
                    }
                  }
                }
              }
            ],
            "timeshiftDuration": "0s",
            "yAxis": {
              "label": "CPU %",
              "scale": "LINEAR"
            }
          }
        }
      },
      {
        "width": 6,
        "height": 4,
        "xPos": 6,
        "widget": {
          "title": "Memory Usage",
          "xyChart": {
            "dataSets": [
              {
                "timeSeriesQuery": {
                  "timeSeriesFilter": {
                    "filter": "resource.type=\"gce_instance\" AND metric.type=\"agent.googleapis.com/memory/percent_used\"",
                    "aggregation": {
                      "alignmentPeriod": "60s",
                      "perSeriesAligner": "ALIGN_MEAN"
                    }
                  }
                }
              }
            ],
            "timeshiftDuration": "0s",
            "yAxis": {
              "label": "Memory %",
              "scale": "LINEAR"
            }
          }
        }
      },
      {
        "width": 12,
        "height": 4,
        "yPos": 4,
        "widget": {
          "title": "Network Traffic",
          "xyChart": {
            "dataSets": [
              {
                "timeSeriesQuery": {
                  "timeSeriesFilter": {
                    "filter": "resource.type=\"gce_instance\" AND metric.type=\"compute.googleapis.com/instance/network/received_bytes_count\"",
                    "aggregation": {
                      "alignmentPeriod": "60s",
                      "perSeriesAligner": "ALIGN_RATE"
                    }
                  }
                },
                "plotType": "LINE",
                "targetAxis": "Y1"
              },
              {
                "timeSeriesQuery": {
                  "timeSeriesFilter": {
                    "filter": "resource.type=\"gce_instance\" AND metric.type=\"compute.googleapis.com/instance/network/sent_bytes_count\"",
                    "aggregation": {
                      "alignmentPeriod": "60s",
                      "perSeriesAligner": "ALIGN_RATE"
                    }
                  }
                },
                "plotType": "LINE",
                "targetAxis": "Y1"
              }
            ],
            "timeshiftDuration": "0s",
            "yAxis": {
              "label": "Bytes/sec",
              "scale": "LINEAR"
            }
          }
        }
      }
    ]
  }
}
EOF

gcloud monitoring dashboards create --config-from-file=/tmp/dashboard.json

echo "==> Monitoring setup completed!"
echo "Dashboard: https://console.cloud.google.com/monitoring/dashboards"
echo "Alerting: https://console.cloud.google.com/monitoring/alerting"

# Clean up temporary files
rm -f /tmp/service-down-policy.yaml /tmp/high-cpu-policy.yaml /tmp/high-memory-policy.yaml /tmp/dashboard.json
