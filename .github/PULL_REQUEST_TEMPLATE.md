## 📝 Description

Please provide a brief summary of the changes introduced by this Pull Request and link any associated issue.

Fixes #(issue)

---

## 🎯 Type of Change

Please mark the relevant options:

- [ ] 🐛 **Bug fix** (non-breaking change fixing an issue)
- [ ] ✨ **New feature** (non-breaking change adding functionality)
- [ ] 💥 **Breaking change** (fix or feature causing existing functionality to not work as expected)
- [ ] ♻️ **Refactoring** (code structure improvement without behavior change)
- [ ] 📚 **Documentation** (updates to `docs/`, `README.md`, or examples)
- [ ] 🧪 **Tests** (unit, integration, fuzz, or e2e test suite additions)
- [ ] 🚀 **CI/CD & Governance** (workflows, Helm charts, or contributor files)

---

## 🧩 Provider & Component Impact

- [ ] Pluggable Secret Ingestion (`internal/source/`, Azure Service Bus, ESO synergy)
- [ ] Synthetic Validation Probes (`internal/probes/`, HTTP, TLS, PostgreSQL, MySQL, Job)
- [ ] Canary Isolation & Sandboxing (`internal/canary/`, NetworkPolicy, Cilium eBPF)
- [ ] Workload Promotion (`internal/workload/`, Deployment, StatefulSet, Rollout)
- [ ] GitOps Integration (`internal/integration/`, Argo CD `ignoreDifferences`)
- [ ] Telemetry & Observability (`pkg/telemetry/`, Prometheus alerts, OTel)
- [ ] Helm Chart & Packaging (`deploy/helm/`)

---

## 📋 Contributor Verification Checklist

Please verify each of the following before requesting review:

- [ ] **Signed-off Commits**: All commits in this PR are signed off (`git commit -s`) in compliance with the **CNCF / Developer Certificate of Origin (DCO)**.
- [ ] **Unit & Integration Tests**: `go test -race ./...` and `envtest` suites execute and pass 100% locally.
- [ ] **Static Analysis**: `golangci-lint run` passes with zero warnings or errors.
- [ ] **CRD Manifests Generated**: If `api/v1alpha1/` types were modified, `make manifests` and `make generate` were run.
- [ ] **Documentation Updated**: Relevant guides in `docs/` or `README.md` have been updated to reflect changes.
- [ ] **Examples Updated**: Relevant manifests in `examples/` are tested and updated (if applicable).
- [ ] **No Credentials Committed**: Verified that no real tokens, passwords, or connection strings are present in code or tests.
