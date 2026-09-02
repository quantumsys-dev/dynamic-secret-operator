# Contributing to Dynamic Secret Operator (DSO)

Thank you for your interest in contributing to **Dynamic Secret Operator (DSO)**! We welcome contributions from the community to make zero-downtime dynamic secret rotation the universal cloud-native standard.

---

## 📜 Code of Conduct

All contributors are expected to adhere to our [Code of Conduct](CODE_OF_CONDUCT.md) (CNCF Contributor Covenant v2.1). Please review it before participating.

---

## 🛠️ Development Prerequisites

To build, test, and run DSO locally, ensure you have the following tools installed:

* **Go**: `1.22+` (matching `go.mod`)
* **Docker / Podman**: Engine for container builds and Kind clusters
* **Kind**: `v0.20+` for running local end-to-end integration tests
* **Kubectl**: `v1.28+`
* **Helm**: `v3.12+`
* **golangci-lint**: `v1.55+` (for static analysis)
* **WSL2 / Linux environment**: For developers running on Windows

---

## 🌿 Branching & Commit Guidelines

### Developer Certificate of Origin (DCO) Sign-Off

All commits **must be signed off** to certify compliance with the [Developer Certificate of Origin (DCO)](https://developercertificate.org/). Use the `-s` flag when committing:

```bash
git commit -s -m "feat(probes): add Redis Sentinel support"
```

### Conventional Commits

Commit messages must follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

* `feat(...)`: New features or capabilities (e.g. `feat(source): add AWS Secrets Manager provider`)
* `fix(...)`: Bug fixes (e.g. `fix(probes): use raw unescaped credentials in MySQL DSN`)
* `docs(...)`: Documentation updates (e.g. `docs: add getting started quickstart`)
* `test(...)`: Unit, integration, or fuzz testing additions
* `refactor(...)`: Code restructuring without changing behavior
* `ci(...)`: GitHub Actions and build automation changes
* `chore(...)`: Maintenance tasks, dependency bumps, or governance updates

---

## ⚙️ Local Development Workflow

### 1. Clone the Repository

```bash
git clone https://github.com/quantumsys-dev/dynamic-secret-operator.git
cd dynamic-secret-operator
```

### 2. Code Generation & CRD Manifests

Whenever you modify CRD types in `api/v1alpha1/`, regenerate the deepcopy code and Kubernetes YAML CRDs:

```bash
# Generate DeepCopy implementations
make generate

# Generate CRD manifests (config/crd/bases/ and deploy/helm/dso/crds/)
make manifests
```

### 3. Running Static Analysis

Run `golangci-lint` to check for formatting, linting, and security issues:

```bash
golangci-lint run --timeout=5m
```

### 4. Running Unit & Integration Tests (envtest)

Controller integration tests run against a real Kubernetes control plane via `setup-envtest`:

```bash
# Download Kubernetes binaries and export KUBEBUILDER_ASSETS
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19
export KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 -p path)"

# Run all tests with race detection enabled
go test -v -race -count=1 ./api/... ./internal/... ./pkg/...
```

### 5. Running End-to-End Tests (Kind)

Test complete operator deployments inside a local disposable Kind cluster:

```bash
# Create local Kind cluster and execute e2e suite
kind create cluster --name dso-e2e --config test/e2e/kind-config.yaml
make test-e2e
kind delete cluster --name dso-e2e
```

---

## 🔒 Security & Safe Coding Rules

1. **Zero Hardcoded Credentials**: Never commit API keys, cloud tokens, or private keys. Use mock strings in tests.
2. **Anti-Leak Error Sanitization**: All database and probe drivers must pass errors through `probes.SanitizeDBError` to strip credentials before logging or tracing.
3. **Detached Contexts for Settlement**: Network settlement calls (like Azure Service Bus `CompleteMessage`) must use detached timeouts (`context.Background()` with timeout) rather than inheriting canceled parent contexts.
4. **No Shadow Structs**: Import official upstream API packages (e.g., `github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1`) rather than redefining structs.

---

## 🚀 Pull Request Checklist

Before submitting a Pull Request, verify that:

- [ ] Commits are atomic, descriptive, follow **Conventional Commits**, and include **`-s` DCO sign-off**.
- [ ] `golangci-lint run` passes with zero warnings.
- [ ] `go test -race ./...` passes 100% of unit and envtest integration tests.
- [ ] New features or bug fixes include corresponding test cases.
- [ ] Documentation ([docs/](docs/) or [README.md](README.md)) is updated if user-facing behavior changes.
- [ ] No temporary debug code, hardcoded secrets, or unintended files are staged.

---

## 💬 Community & Help

* **Issue Tracker**: [GitHub Issues](https://github.com/quantumsys-dev/dynamic-secret-operator/issues)
* **Discussions**: [GitHub Discussions](https://github.com/quantumsys-dev/dynamic-secret-operator/discussions)
* **Security Reporting**: [SECURITY.md](SECURITY.md) / `security@quantumsys.dev`
