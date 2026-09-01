# ESO + Job-Based Redis Probe Validation

This example demonstrates using an ephemeral **batch/v1.Job** validation probe with **Dynamic Secret Operator (DSO)** to validate custom protocol (Redis AUTH) secret rotations synchronized from any vault backend via **External Secrets Operator (ESO)**.

---

## Deploying

```bash
chmod +x deploy.sh
./deploy.sh
```
