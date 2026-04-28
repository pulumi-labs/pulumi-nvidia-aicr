# Contributing

Thanks for your interest in contributing to the Pulumi NVIDIA AICR provider.

## Development setup

You will need:

- [Go](https://go.dev/) 1.24+
- [Pulumi CLI](https://www.pulumi.com/docs/install/) 3.165+
- For SDK generation/build: Node.js 18+, Python 3.9+, .NET 8+, JDK 11+

Clone, build, and test:

```bash
git clone https://github.com/pulumi-labs/pulumi-nvidia-aicr.git
cd pulumi-nvidia-aicr
make provider          # build the provider binary into ./bin/
make test              # run unit tests
make schema            # regenerate provider/cmd/.../schema.json
make sdks              # regenerate all 5 language SDKs
```

## Repository layout

```
provider/
  cmd/pulumi-resource-nvidia-aicr/  # provider binary entry point + schema.json
  pkg/provider/                     # ClusterStack component implementation
  pkg/recipe/                       # AICR recipe resolver and overlay engine
  pkg/recipes/                      # vendored AICR recipe data (go:embed)
  pkg/version/                      # version string injected via ldflags
sdk/
  nodejs/ python/ go/ dotnet/ java/ # generated SDKs (do not edit by hand)
examples/                           # one directory per scenario × language
.github/workflows/                  # CI (test.yml) and release (release.yml)
```

## Pull request flow

1. Open an issue first for non-trivial changes so we can align on direction.
2. Make your changes on a topic branch off `main`.
3. Run `make test` and, if you touched the schema or component API,
   `make schema && make sdks` and commit the regenerated files.
4. Open a PR. CI runs the full test + SDK build matrix.
5. A maintainer reviews and merges.

## Updating the embedded AICR recipe data

Recipe YAML files in `provider/pkg/recipes/` are vendored from
[nvidia/aicr](https://github.com/nvidia/aicr) and embedded at compile time
via `go:embed`. To pick up new upstream recipes:

1. Fetch the latest YAML files from the AICR repo at the tag/commit you
   want.
2. Replace the contents of `provider/pkg/recipes/{overlays,mixins,registry.yaml,...}`.
3. If you added a new overlay with new criteria values, update the
   `Annotate` descriptions in `provider/pkg/provider/clusterstack.go`,
   the `validateArgs` helper, the README's "Supported Configurations"
   table, and `examples/README.md`.
4. Bump `recipeVersion` in `provider/pkg/recipe/resolver.go` to track the
   AICR data version this provider build embeds.
5. Run `make test` — recipe-resolution tests will catch most schema drift.

## Releasing

Releases are tag-driven. Pushing a `v*.*.*` tag fires
`.github/workflows/release.yml`, which:

1. Cross-compiles the provider for `linux/amd64`, `linux/arm64`,
   `darwin/amd64`, `darwin/arm64`, and `windows/amd64`.
2. Generates SDKs in all five languages with the tag's version baked in.
3. Uploads provider tarballs and SDK source archives to the GitHub Release.
4. Optionally publishes SDKs to npm/PyPI/NuGet (disabled by default;
   re-enable by removing `if: false` and setting the relevant secrets).

## Code style

- Go: standard `go fmt` + `go vet`. Match existing patterns; no unrelated
  refactors in feature PRs.
- Comments on exported symbols only when the WHY is non-obvious.
- Keep tests focused: the recipe-resolution layer has thorough coverage in
  `provider/pkg/recipe/`; the component layer has smoke tests in
  `provider/pkg/provider/clusterstack_test.go`.

## Reporting bugs

Open an issue with the bug template. Please include the provider version,
your `accelerator`/`service`/`intent` inputs, and the relevant `pulumi up`
output.

## Code of Conduct

This project follows the [Contributor Covenant](https://www.contributor-covenant.org/).
