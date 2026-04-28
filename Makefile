PROJECT_NAME := nvidia-aicr
PROVIDER     := pulumi-resource-$(PROJECT_NAME)
VERSION      ?= 0.0.1-dev
PROVIDER_DIR := provider
BIN_DIR      := bin
SCHEMA_FILE  := $(PROVIDER_DIR)/cmd/$(PROVIDER)/schema.json

LDFLAGS := -X github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/version.Version=$(VERSION)

.PHONY: all provider schema test lint clean install \
        nodejs_sdk python_sdk go_sdk dotnet_sdk java_sdk sdks \
        build_nodejs_sdk build_python_sdk build_dotnet_sdk build_java_sdk \
        set_version sdk_fixups

all: provider

# Build the provider binary
provider:
	cd $(PROVIDER_DIR) && go build -ldflags "$(LDFLAGS)" -o ../$(BIN_DIR)/$(PROVIDER) ./cmd/$(PROVIDER)

# Generate the provider schema by querying the built provider plugin via the
# Pulumi CLI. The provider binary itself only exposes the gRPC
# ResourceProvider service; pulumi-go-provider has no built-in `schema`
# subcommand.
schema: provider
	pulumi package get-schema ./$(BIN_DIR)/$(PROVIDER) >$(SCHEMA_FILE)

# Run unit tests
test:
	go test -v -count=1 -race ./provider/pkg/...

# Run linter
lint:
	go vet ./...

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
	cd sdk/go/nvidiaaicr && go mod init github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr && go mod tidy

# Generate .NET SDK
dotnet_sdk: schema
	rm -rf sdk/dotnet
	pulumi package gen-sdk --language dotnet $(SCHEMA_FILE)

# Generate Java SDK
java_sdk: schema
	rm -rf sdk/java
	pulumi package gen-sdk --language java $(SCHEMA_FILE)

# Generate all SDKs
sdks: nodejs_sdk python_sdk go_sdk dotnet_sdk java_sdk sdk_fixups

# Re-apply tweaks that pulumi package gen-sdk does not (yet) emit:
#   - Python: point readme at the package-local README and use the SPDX
#     string-form license (the generator emits a deprecated [project.license]
#     table that ships an empty PyPI long description and prints a warning).
#   - .NET: declare PackageReadmeFile and pack README.md so NuGet stops
#     warning about a missing readme.
sdk_fixups:
	@if [ -f sdk/python/pyproject.toml ]; then \
		python3 -c 'import re,sys,pathlib; \
p=pathlib.Path("sdk/python/pyproject.toml"); s=p.read_text(); \
s=re.sub(r"^( *)readme = \"README\\.md\"", r"\1readme = \"pulumi_nvidia_aicr/README.md\"", s, flags=re.M); \
s=re.sub(r"\\n  \\[project\\.license\\]\\n    text = \"Apache-2\\.0\"", "\\n  license = \"Apache-2.0\"", s); \
p.write_text(s)'; \
	fi
	@if [ -f sdk/dotnet/Pulumi.NvidiaAicr.csproj ] && ! grep -q PackageReadmeFile sdk/dotnet/Pulumi.NvidiaAicr.csproj; then \
		sed -i.bak -E 's|(<PackageIcon>logo.png</PackageIcon>)|\1\n    <PackageReadmeFile>README.md</PackageReadmeFile>|' sdk/dotnet/Pulumi.NvidiaAicr.csproj && \
		rm -f sdk/dotnet/Pulumi.NvidiaAicr.csproj.bak; \
	fi
	@if [ -f sdk/dotnet/Pulumi.NvidiaAicr.csproj ] && ! grep -q '<None Include="README.md">' sdk/dotnet/Pulumi.NvidiaAicr.csproj; then \
		python3 -c 'import pathlib; \
p=pathlib.Path("sdk/dotnet/Pulumi.NvidiaAicr.csproj"); s=p.read_text(); \
needle="<None Include=\"logo.png\">\n      <Pack>True</Pack>\n      <PackagePath></PackagePath>\n    </None>"; \
addition=needle + "\n    <None Include=\"README.md\">\n      <Pack>True</Pack>\n      <PackagePath></PackagePath>\n    </None>"; \
p.write_text(s.replace(needle, addition, 1))'; \
	fi

# Compile each SDK to catch generation breaks. These targets are best-effort:
# they no-op if the corresponding toolchain is not installed locally.
build_nodejs_sdk:
	@if command -v npm >/dev/null 2>&1 && [ -d sdk/nodejs ]; then \
		cd sdk/nodejs && npm install && npm run build; \
	else \
		echo "skipping nodejs SDK build (npm not installed or sdk/nodejs missing)"; \
	fi

build_python_sdk:
	@if command -v python3 >/dev/null 2>&1 && [ -d sdk/python ]; then \
		cd sdk/python && python3 -m build --sdist --wheel --outdir dist/; \
	else \
		echo "skipping python SDK build (python3 not installed or sdk/python missing)"; \
	fi

build_dotnet_sdk:
	@if command -v dotnet >/dev/null 2>&1 && [ -d sdk/dotnet ]; then \
		cd sdk/dotnet && dotnet build; \
	else \
		echo "skipping dotnet SDK build (dotnet not installed or sdk/dotnet missing)"; \
	fi

build_java_sdk:
	@if command -v gradle >/dev/null 2>&1 && [ -d sdk/java ]; then \
		cd sdk/java && gradle build; \
	else \
		echo "skipping java SDK build (gradle not installed or sdk/java missing)"; \
	fi

# Substitute the release VERSION into language-SDK manifests.
# Note: in the normal flow, `make sdks VERSION=$(VERSION)` already produces SDKs
# with the right version baked in (the provider binary embeds VERSION via
# ldflags, schema inherits it, SDK generators read it from the schema). This
# target is a fallback for when SDKs have been generated with a placeholder
# version (e.g., running `make sdks` without VERSION set, then publishing).
set_version:
	@if [ -f sdk/nodejs/package.json ]; then \
		sed -i.bak -E 's/"version": "[^"]+"/"version": "$(VERSION)"/' sdk/nodejs/package.json && \
		rm -f sdk/nodejs/package.json.bak; \
	fi
	@if [ -f sdk/python/pyproject.toml ]; then \
		sed -i.bak -E 's/^( *version *= *)"[^"]+"/\1"$(VERSION)"/' sdk/python/pyproject.toml && \
		rm -f sdk/python/pyproject.toml.bak; \
	fi
	@if [ -f sdk/dotnet/Pulumi.NvidiaAicr.csproj ]; then \
		sed -i.bak -E 's|<Version>[^<]+</Version>|<Version>$(VERSION)</Version>|' sdk/dotnet/Pulumi.NvidiaAicr.csproj && \
		rm -f sdk/dotnet/Pulumi.NvidiaAicr.csproj.bak; \
	fi
	@if [ -f sdk/java/build.gradle ]; then \
		sed -i.bak -E 's/version = "[^"]+"/version = "$(VERSION)"/' sdk/java/build.gradle && \
		rm -f sdk/java/build.gradle.bak; \
	fi

# Install the provider binary locally
install: provider
	cp $(BIN_DIR)/$(PROVIDER) $(GOPATH)/bin/

# Clean build artifacts
clean:
	rm -rf $(BIN_DIR) sdk/nodejs sdk/python sdk/go sdk/dotnet sdk/java
	rm -f $(SCHEMA_FILE)
