# Embedded AICR Recipe Data

This directory contains recipe data vendored from
[NVIDIA/aicr](https://github.com/NVIDIA/aicr) and embedded into the provider
binary at compile time via `data.go`.

## Layout

| Path | Used by resolver | Purpose |
|---|---|---|
| `registry.yaml` | yes | Component registry: chart repos, versions, namespaces |
| `overlays/*.yaml` | yes | Recipe overlays + base recipe + leaf recipes |
| `mixins/*.yaml` | yes | Composable recipe fragments (os, platform) |
| `components/*/values*.yaml` | yes | Default Helm values per component |
| `components/*/manifests/` | no | Raw manifests (AICR manifest-only mode, not used here) |
| `checks/` | no | AICR validator definitions (not used here) |
| `validators/` | no | AICR validator catalog (not used here) |

The unused directories are kept for parity with upstream AICR. Only files
matched by the `go:embed` directive in `data.go` are shipped in the binary.

## Updating Recipes

To pull newer recipe data from upstream:

1. Replace files in this directory with the corresponding files from
   the AICR repo (preserve the same layout).
2. Run `make test` to ensure resolution still works.
3. If the upstream registry schema changed, update `provider/pkg/recipe/types.go`
   to match.
