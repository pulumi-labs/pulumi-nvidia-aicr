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
  `Namespace` child resources for each resolved component. For *recipe
  component deployment* the SDK is used for resolution and bundling only —
  deployment always stays in Pulumi (never use the SDK's own component
  deployers). The SDK's *validation harness* (short-lived snapshot-agent and
  validator Jobs) is the deliberate exception: that is the validation
  mechanism used by ValidationRun, not stack deployment
- **ValidationRun** custom resource runs a recipe's empirical validation
  (snapshot + deployment/conformance/performance checks) against a live
  cluster at create time via `aicr.Validate`, recording per-check results in
  state
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
- `provider/pkg/provider/validationrun.go` — ValidationRun custom resource
- `provider/pkg/provider/provider.go` — Provider registration
- `provider/pkg/provider/manifest_render.go` — Helm-engine rendering of recipe
  manifest bundles (the SDK returns them as un-rendered Helm templates)
- `provider/pkg/aicr/aicr.go` — SDK adapter (resolve, bundle, ordering, overrides)
- `provider/pkg/aicr/validate.go` — SDK adapter for validation (snapshot,
  ValidateState, CTRF translation, readiness classification, orphan-Job sweep)
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
  `validateCriteria` / `validateCompatibility`) so users get precise errors
  before SDK resolution; ValidationRun shares `validateCriteria` so both
  resources produce byte-identical criteria errors

## ValidationRun

- The SDK creates the validation namespace (default `aicr-validation`) on
  first run and never deletes it; a leftover `aicr-snapshot` ConfigMap also
  persists. `Delete` is a deliberate no-op — everything else is reaped by the
  SDK per run (including on cancellation), plus an adapter-side best-effort
  sweep of runID-labeled validator Jobs
- The kubeconfig identity needs RBAC to create/patch Namespaces, ClusterRoles,
  and ClusterRoleBindings (the validator uses a per-run cluster-admin binding,
  reaped after the run)
- Any input change replaces the resource (explicit `Diff`, every changed
  top-level property is `UpdateReplace`); with `strict: true` a failed run
  persists full results via `infer.ResourceInitFailedError` and re-runs on
  every subsequent `pulumi up` until it passes (engine init-error semantics)
- Always pass explicit validation phases to the SDK (`WithValidationPhases`);
  its default runs ALL phases including performance
- Readiness failures are classified by pinned message prefixes on the SDK's
  `StructuredError` (no typed sentinel exists); a regression test drives the
  real `checkReadiness` path offline so SDK message drift breaks CI — run the
  adapter tests on every SDK bump
- **Never name a custom-resource input `version`**: pulumi-go-provider's
  infer diff path deletes any input property with that name before decoding
  (engine-injected provider version), producing a perpetual spurious replace
  that only manifests live. That is why the assertion input is
  `recipeDataVersion`. Wire-level lifecycle tests
  (`validationrun_wire_test.go`) guard the diff path; keep them when adding
  resources
