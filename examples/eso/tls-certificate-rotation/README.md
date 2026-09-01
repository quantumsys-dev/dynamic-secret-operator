# ESO + TLS Certificate Rotation & Live Handshake Probing

This example demonstrates zero-downtime TLS certificate rotation with **External Secrets Operator (ESO)** and **Dynamic Secret Operator (DSO)**, utilizing synthetic TLS probes that perform real TLS handshakes and thumbprint validation on canary pods before production promotion.

---

## Deploying

```bash
chmod +x deploy.sh
./deploy.sh
```
