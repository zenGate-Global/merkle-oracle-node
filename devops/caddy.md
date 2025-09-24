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

## Network + DNS
- Automated by pipeline: assign external IP (if missing), add network tag, and create firewall rule for ports 80/443.
- Your action: add a DNS A record pointing to the VM’s external IP.

Find the external IP (project: palm-portal-staging):
```bash
gcloud compute instances describe merkle-oracle-ubuntu \
  --project=palm-portal-staging \
  --zone=europe-west1-b \
  --format='get(networkInterfaces[0].accessConfigs[0].natIP)'
```

Add DNS A record (project: zengate-dns-management, zone: zengate-dev):
```bash
gcloud dns record-sets create merkle-staging.zengate-dev.com. \
  --project=zengate-dns-management \
  --zone=zengate-dev \
  --type=A \
  --ttl=300 \
  --rrdatas=<EXTERNAL_IP>
```

After DNS propagates and ports 80/443 are reachable, Caddy will fetch a certificate automatically, and HTTPS will work.
