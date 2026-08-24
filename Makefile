.PHONY: all build clean test fmt vet lint

all: fmt vet lint build

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
