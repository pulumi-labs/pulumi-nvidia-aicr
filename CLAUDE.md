# CLAUDE.md — Development Guide

## Project Overview

Pulumi component provider for NVIDIA AI Cluster Runtime (AICR). Deploys validated
GPU software stack recipes on Kubernetes clusters via Helm releases.

## Architecture

- **Go component provider** using `pulumi-go-provider` with `infer.ComponentF`
- **Recipe engine** at `provider/pkg/recipe/` resolves AICR criteria → component list
- **Embedded data** at `provider/pkg/recipes/` contains AICR recipe YAML files via `go:embed`
- **ClusterStack** component creates `helm.Release` child resources for each resolved component
- **Multi-language SDKs** generated from schema via `pulumi package gen-sdk`

## Build & Test

```bash
cd provider

# Build
go build ./...

# Test
go test -v ./pkg/recipe/...

# Full provider binary
cd .. && make provider
```

## Key Files

- `provider/pkg/provider/clusterstack.go` — Main component resource
- `provider/pkg/provider/provider.go` — Provider registration
- `provider/pkg/recipe/resolver.go` — Recipe resolution engine
- `provider/pkg/recipe/overlay.go` — YAML overlay merge logic
- `provider/pkg/recipes/` — Embedded AICR recipe data (overlays, registry, values)

## Recipe Data

Recipe files are vendored from https://github.com/nvidia/aicr and embedded at compile time.
To update recipes:
1. Fetch new YAML files from the AICR repo
2. Replace files in `provider/pkg/recipes/`
3. Run tests to verify resolution still works

## Adding a New Cloud Service

1. Add overlay file in `provider/pkg/recipes/overlays/<service>.yaml`
2. Add leaf recipe files for accelerator/intent/platform combinations
3. Add any cloud-specific components to `registry.yaml`
4. Run tests

## Conventions

- Recipe criteria fields are plain Go strings (not `pulumi.StringInput`) because
  they must be known at plan time
- Kubeconfig is `pulumi.StringPtrInput` to accept outputs from cluster resources
- Components are deployed in topological order using `pulumi.DependsOn`
