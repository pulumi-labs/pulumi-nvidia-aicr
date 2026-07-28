# CLAUDE.md — Development Guide

## Project Overview

Pulumi component provider for NVIDIA AI Cluster Runtime (AICR). Deploys validated
GPU software stack recipes on Kubernetes clusters via Helm releases.

## Architecture

- **Go component provider** using `pulumi-go-provider` with `infer.ComponentF`
- **Recipe resolution delegates to the official AICR Go SDK**
  (`github.com/NVIDIA/aicr/pkg/client/v1`, pinned in `go.mod`) against the
  SDK's embedded recipe data — no vendored recipe data in this repo
- **Adapter** at `provider/pkg/aicr/` wraps the SDK client: resolve + bundle,
  deployment ordering from the SDK's `DeploymentOrder`/`DependencyRefs`,
  pre-manifest loading, and user override/skip post-processing
- **ClusterStack** component creates `helm.Release` / `ConfigGroup` /
  `Namespace` child resources for each resolved component. The SDK is used for
  resolution and bundling only — deployment always stays in Pulumi (never use
  the SDK's own deployers)
- **Multi-language SDKs** generated from schema via `pulumi package gen-sdk`

## Build & Test

```bash
# Build
go build ./...

# Test (adapter + provider)
go test -v ./provider/pkg/...

# Full provider binary
make provider

# Regenerate schema after any API change (CI diffs it against the committed file)
make schema
```

## Key Files

- `provider/pkg/provider/clusterstack.go` — Main component resource
- `provider/pkg/provider/provider.go` — Provider registration
- `provider/pkg/provider/manifest_render.go` — Helm-engine rendering of recipe
  manifest bundles (the SDK returns them as un-rendered Helm templates)
- `provider/pkg/aicr/aicr.go` — SDK adapter (resolve, bundle, ordering, overrides)
- `provider/cmd/pulumi-resource-nvidia-aicr/schema.json` — Committed schema

## Recipe Data

Recipe data is embedded in the AICR SDK module and versioned by it. To pick up
new recipes, bump `github.com/NVIDIA/aicr` in `go.mod` to a newer release tag
(`go get github.com/NVIDIA/aicr@<tag> && go mod tidy`), run the tests, and
update the README's "AICR Version Compatibility" table. Never vendor or edit
NVIDIA recipe YAML in this repo.

New cloud services, accelerators, and platforms arrive the same way — through
SDK upgrades. When one lands, extend the `supported*` allowlists and
`validateCompatibility` in `clusterstack.go` (they encode the provider's
published support matrix) and add adapter tests for the new combination.

## Conventions

- Recipe criteria fields are plain Go strings (not `pulumi.StringInput`) because
  they must be known at plan time
- Kubeconfig is `pulumi.StringPtrInput` to accept outputs from cluster resources
- Components are deployed in the SDK's topological deployment order, with
  `pulumi.DependsOn` wired from the SDK's per-component dependency lists
- The `os` input has no default: unset means OS-agnostic resolution (required
  for `kind`, which binds no OS); some combinations require an explicit OS
  (gke → `cos`, platform recipes → `ubuntu`)
- Friendly validation stays provider-side (`validateArgs` /
  `validateCompatibility`) so users get precise errors before SDK resolution
