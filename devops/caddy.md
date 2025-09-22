# Why we use Caddy for HTTPS (staging)

Goal: serve the app at merkle-staging.zengate-dev.com with HTTPS, low cost, no Google Load Balancer.

- Custom domains on GCE without a load balancer need a web server on the VM to handle TLS.
- Google-managed HTTPS for custom domains typically requires a Load Balancer or Cloud Run.
- To keep costs minimum and stay on a single VM, we run a lightweight reverse proxy (Caddy) next to the app.
- Caddy gets and renews free Let’s Encrypt certificates automatically and proxies to the app container.

## What the deploy does
- Starts/updates the app container (no host ports; on docker network `merkle-net`).
- Starts/updates a `caddy` container that:
  - Listens on ports 80 and 443 on the VM
  - Uses a simple Caddyfile to reverse proxy to `app:8080`
  - Automatically provisions TLS for `merkle-staging.zengate-dev.com`

## One-time network + DNS steps
You (or an admin) need to do these once in Google Cloud for public HTTPS:

1) Give the VM an external IP (project: palm-portal-staging)
- If the instance has no external IP, add one:

```bash
# Assign an ephemeral external IP (no VM restart needed)
gcloud compute instances add-access-config merkle-oracle-ubuntu \
  --zone=europe-west1-b
```

2) Open firewall for HTTP/HTTPS to this VM
- Allow TCP 80 and 443 inbound to the instance. Example using a network tag:

```bash
# Add a network tag to the instance (once)
gcloud compute instances add-tags merkle-oracle-ubuntu \
  --zone=europe-west1-b \
  --tags=web-https

# Create firewall rule targeting that tag (once)
gcloud compute firewall-rules create allow-http-https \
  --allow=tcp:80,tcp:443 \
  --target-tags=web-https \
  --direction=INGRESS \
  --priority=1000
```

3) Add DNS A record (project: zengate-dns-management, zone: zengate-dev)
- Point the domain to the VM’s external IP (replace X.X.X.X):

```bash
gcloud dns record-sets create merkle-staging.zengate-dev.com. \
  --project=zengate-dns-management \
  --zone=zengate-dev \
  --type=A \
  --ttl=300 \
  --rrdatas=X.X.X.X
```

After DNS propagates and ports 80/443 are reachable, Caddy will fetch a certificate automatically, and HTTPS will work.

Notes
- Keep ngrok running if you want a fallback URL during DNS propagation.
- This keeps costs minimal: only a single VM; no external load balancer.

