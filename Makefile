.PHONY: all build clean test fmt vet lint manifests generate

all: manifests generate fmt vet lint build

# Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

# Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.16.5

manifests:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases output:webhook:artifacts:config=config/webhook

generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) object:headerFile="hack/boilerplate.go.txt" paths="./..."

build:
	go build -o bin/dso-manager ./cmd/main.go

clean:
	go clean
	rm -rf bin/

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...
