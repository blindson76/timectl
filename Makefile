.PHONY: help build build-all clean test run-tests install-deps lint fmt proto generate-pb

help:
	@echo "timectl - Distributed Time Control System"
	@echo ""
	@echo "Available targets:"
	@echo "  install-deps    Install dependencies"
	@echo "  proto          Generate protobuf files"
	@echo "  build          Build timectl binary"
	@echo "  build-all      Build for multiple platforms"
	@echo "  clean          Clean build artifacts"
	@echo "  test           Run tests"
	@echo "  fmt            Format code"
	@echo "  lint           Run linter"
	@echo "  run-node1      Run single test node"
	@echo "  start-cluster  Start 3-node test cluster"
	@echo "  stop-cluster   Stop test cluster"

install-deps:
	go mod download
	go mod tidy

proto:
	@echo "Generating protobuf files..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		pkg/pb/timectl.proto

generate-pb: proto
	@echo "Protobuf files generated"

build: generate-pb
	@mkdir -p bin
	go build -o bin/timectl ./cmd/timectl

build-all: generate-pb
	@mkdir -p bin
	@echo "Building for Linux..."
	GOOS=linux GOARCH=amd64 go build -o bin/timectl-linux-amd64 ./cmd/timectl
	@echo "Building for Windows..."
	GOOS=windows GOARCH=amd64 go build -o bin/timectl-windows-amd64.exe ./cmd/timectl
	@echo "Building for macOS..."
	GOOS=darwin GOARCH=amd64 go build -o bin/timectl-darwin-amd64 ./cmd/timectl
	GOOS=darwin GOARCH=arm64 go build -o bin/timectl-darwin-arm64 ./cmd/timectl

clean:
	rm -rf bin/
	rm -rf data/
	find . -name "*.pb.go" -delete
	find . -name "*_pb2.py" -delete

test:
	go test -v -coverprofile=coverage.out ./...

fmt:
	go fmt ./...

lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

run-node1: build
	@mkdir -p data/node1
	./bin/timectl \
		--node-id node1 \
		--raft-addr localhost:5000 \
		--rpc-addr localhost:5001 \
		--http-addr localhost:8080 \
		--data-dir ./data/node1 \
		-v

run-node2: build
	@mkdir -p data/node2
	./bin/timectl \
		--node-id node2 \
		--raft-addr localhost:5010 \
		--rpc-addr localhost:5011 \
		--http-addr localhost:8081 \
		--data-dir ./data/node2 \
		--join localhost:5000 \
		-v

run-node3: build
	@mkdir -p data/node3
	./bin/timectl \
		--node-id node3 \
		--raft-addr localhost:5020 \
		--rpc-addr localhost:5021 \
		--http-addr localhost:8082 \
		--data-dir ./data/node3 \
		--join localhost:5000 \
		-v

start-cluster: clean build
	@echo "Starting 3-node cluster..."
	@bash scripts/start-cluster.sh

stop-cluster:
	@pkill -f "timectl" || true
	@echo "Cluster stopped"

.PHONY: help install-deps proto build build-all clean test fmt lint run-node1 run-node2 run-node3 start-cluster stop-cluster generate-pb
