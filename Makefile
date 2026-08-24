.PHONY: all build clean test fmt vet

all: fmt vet build

build:
	go build -o bin/dso-manager ./cmd/dso-manager/main.go

clean:
	go clean
	rm -rf bin/

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...
