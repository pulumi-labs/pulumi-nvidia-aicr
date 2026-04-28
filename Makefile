PROJECT_NAME := nvidia-aicr
PROVIDER     := pulumi-resource-$(PROJECT_NAME)
VERSION      ?= 0.0.1-dev
PROVIDER_DIR := provider
BIN_DIR      := bin
SCHEMA_FILE  := $(PROVIDER_DIR)/cmd/$(PROVIDER)/schema.json

LDFLAGS := -X github.com/pulumi/pulumi-nvidia-aicr/provider/pkg/version.Version=$(VERSION)

.PHONY: all provider schema test lint clean install

all: provider

# Build the provider binary
provider:
	cd $(PROVIDER_DIR) && go build -ldflags "$(LDFLAGS)" -o ../$(BIN_DIR)/$(PROVIDER) ./cmd/$(PROVIDER)

# Generate the provider schema
schema: provider
	./$(BIN_DIR)/$(PROVIDER) schema >$(SCHEMA_FILE)

# Run unit tests
test:
	cd $(PROVIDER_DIR) && go test -v -count=1 -race ./pkg/...

# Run linter
lint:
	cd $(PROVIDER_DIR) && go vet ./...

# Generate Node.js SDK
nodejs_sdk: schema
	rm -rf sdk/nodejs
	pulumi package gen-sdk --language nodejs $(SCHEMA_FILE)

# Generate Python SDK
python_sdk: schema
	rm -rf sdk/python
	pulumi package gen-sdk --language python $(SCHEMA_FILE)

# Generate Go SDK
go_sdk: schema
	rm -rf sdk/go
	pulumi package gen-sdk --language go $(SCHEMA_FILE)

# Generate .NET SDK
dotnet_sdk: schema
	rm -rf sdk/dotnet
	pulumi package gen-sdk --language dotnet $(SCHEMA_FILE)

# Generate all SDKs
sdks: nodejs_sdk python_sdk go_sdk dotnet_sdk

# Install the provider binary locally
install: provider
	cp $(BIN_DIR)/$(PROVIDER) $(GOPATH)/bin/

# Clean build artifacts
clean:
	rm -rf $(BIN_DIR) sdk/nodejs sdk/python sdk/go sdk/dotnet sdk/java
	rm -f $(SCHEMA_FILE)
