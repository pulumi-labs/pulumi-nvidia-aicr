# Design: `nvidia-aicr:index:ValidationRun`

Status: **draft** — for discussion. Informed by the v0.18.0 SDK migration and
the live EKS/H100-recipe test campaign (July–August 2026).

## Problem

`ClusterStack` deploys an AICR recipe, but "deployed" is not "validated."
AICR recipes carry a validation specification — per-component deployment
health checks, conformance checks (`gpu-operator-health`, `dra-support`,
`gang-scheduling`, `accelerator-metrics`, …), and performance checks (NCCL
benchmarks) — that the provider deliberately does not run: they are active
operations against the live cluster, not resource configuration.

The live test campaign showed this gap is where real defects hide. Empirical
validation (run manually via the `aicr` CLI) caught, on a stack that had
deployed "successfully":

- a node-OS mismatch (recipe criteria `os=ubuntu`, actual nodes Amazon
  Linux 2023) that no deploy-time check can see;
- a container-isolation regression (`secure-accelerator-access`) introduced
  by a `driver.enabled=false` values override;
- a broken DRA kubelet plugin (`nvidiaDriverRoot` pointing at a path that
  exists only under operator-managed drivers).

Users should be able to get this signal from `pulumi up`, recorded in stack
state and history, without shelling out to a CLI.

## Decision summary (from prior design discussion)

Validation splits into two different kinds of check that belong in different
places:

1. **Config-level invariants** (combination matrix, allowlists) — knowable at
   plan time. Already owned by the provider (`validateArgs` /
   `validateCompatibility`). Org-specific rules on top of that (e.g. "prod
   may not skip kai-scheduler") are CrossGuard policy-pack territory. Out of
   scope here.
2. **Empirical validation** (snapshot + checks against the live cluster) —
   a post-condition of deployment. Belongs in the stack, **as its own
   resource**, not inside `ClusterStack`.

Why not a `validate: true` input on `ClusterStack`: failure semantics. If a
conformance check fails, the *infrastructure* deployed fine; marking the
ClusterStack failed would poison update semantics and block subsequent
`pulumi up`s of a healthy cluster. A flaky performance check must never
wedge the cluster resource.

Why not a policy pack: policies evaluate the resource graph at preview/update
time; they cannot deploy a snapshotter Job and wait minutes for NCCL results,
and should not.

A separate resource gives us: validation recorded per-deployment in state and
the Console; independent retry (`pulumi up --replace` on the ValidationRun
without touching the cluster); opt-in cost; and a clean strict-mode choice.

## SDK surface (verified, v0.18.0)

The facade `github.com/NVIDIA/aicr/pkg/client/v1` already exposes everything
needed, on the same client the adapter uses for resolve/bundle:

- `CollectSnapshot(ctx, *AgentConfig) (*Snapshot, error)` — deploys a
  short-lived agent Job (privileged by default, temporary cluster-admin
  binding, self-cleaning) and captures cluster state.
- `ValidateState(ctx, *RecipeResult, *Snapshot, ...) ([]*PhaseResult, error)`
  — runs the recipe's validation phases; validator Jobs are deployed to the
  cluster.
- `MergeReports([]*PhaseResult) *ctrf.Report` — normalized results.

The ownership rule applies: the `RecipeResult` passed to `ValidateState` must
come from the same `Client` — so the resource resolves the recipe itself
(same criteria inputs as ClusterStack) rather than accepting a foreign
recipe object.

## Observed behavior to design around (from the live runs)

- **Readiness pre-flight**: before spending money on validator Jobs, the SDK
  checks recipe constraints against the snapshot (k8s version, node OS, GPU
  model). A mismatch aborts the run with `ErrCodeInvalidRequest` — e.g.
  `OS.release.ID expected ubuntu, got amzn`. This is a *distinct outcome*:
  "cluster doesn't match the recipe," not "checks failed" and not "error."
  The resource must surface it as such.
- **Runtime**: deployment + conformance ≈ 10 minutes on a 1-node cluster
  (8m21s + 1m36s observed). Performance phase is materially longer and
  requires recipe-matched GPU hardware. Timeouts must be generous and
  configurable; phases must be selectable.
- **Result shape**: per-validator `{name, phase, status: passed|failed|
  skipped|other, message}`. "other" occurs (e.g. a validator pod cleanup
  race) and must not be collapsed into pass or fail.
- **Cluster access**: everything runs through kubeconfig. Same input
  conventions as ClusterStack (contents vs path vs ambient, context).

## Proposed resource

A **custom resource** (`infer.CustomResource`, not a component) — it has real
create-time work and outputs, but creates no children.

```yaml
type: nvidia-aicr:index:ValidationRun
inputs:
  # Recipe criteria — identical semantics to ClusterStack. Typically copied
  # from the same config the ClusterStack uses.
  accelerator: h100          # required
  service: eks               # required
  intent: training           # required
  os: ubuntu                 # optional; same unset-is-OS-agnostic semantics
  platform: kubeflow         # optional
  nodes: 2                   # optional

  # Cluster access — identical to ClusterStack.
  kubeconfig: <Input[string]>
  kubeconfigPath: <string>
  context: <string>

  # What to run.
  phases: [deployment, conformance]   # default; "performance" opt-in
  strict: false              # default: checks failing does NOT fail the resource
  requireGpu: true           # snapshot agent lands on a GPU node, fails loudly if none
  namespace: aicr-validation # namespace for agent/validator Jobs
  timeoutMinutes: 30

  # Re-run control (command.local.Command convention): any change to
  # triggers replaces the resource, re-running validation.
  triggers: [<Input[any]>]
outputs:
  status: passed | failed | readiness-failed   # rollup
  readinessMessage: ""       # populated iff status == readiness-failed
  phaseResults:              # structured, per validator
    - { name: check-nvidia-smi, phase: deployment, status: passed, message: "" }
    - { name: dra-support, phase: conformance, status: failed, message: "..." }
  passed: 9
  failed: 3
  skipped: 1
  runId: "20260804-140434-..."
  completedAt: "..."
```

### Lifecycle semantics

| Operation | Behavior |
|---|---|
| Create | Resolve recipe → snapshot → validate → set outputs. With `strict: false` (default), the resource **succeeds** even when checks fail — results are data. With `strict: true`, any failed check (or readiness failure) fails the resource. |
| Preview | No-op; all outputs unknown. Validation never runs at preview. |
| Update / triggers change | Replace (delete is free) → fresh run. |
| Delete | No-op. The SDK's agent and validator Jobs self-clean; nothing persists. |
| Refresh | No-op (results are a point-in-time record, not drifting state). |

Gating pattern for users who want deploy-blocked-on-validation: make
downstream resources `dependsOn` a `strict: true` ValidationRun. Teams that
want observation without gating use the default and read outputs.

### Wiring in a program

```python
stack = aicr.ClusterStack("gpu", kubeconfig=cluster.kubeconfig_json, ...)

validation = aicr.ValidationRun("gpu-validation",
    accelerator="h100", service="eks", intent="training",
    os="ubuntu", platform="kubeflow",
    kubeconfig=cluster.kubeconfig_json,
    triggers=[stack.deployed_components],   # re-validate when the stack changes
    opts=pulumi.ResourceOptions(depends_on=[stack]),
)
pulumi.export("validation", validation.phase_results)
```

`triggers=[stack.deployed_components]` gives the natural cadence: validation
re-runs when the deployed set changes, not on every `up`.

## Implementation sketch

- `provider/pkg/aicr/validate.go` — adapter extension: one function
  `Validate(ctx, criteria, kubeconfig, opts) (*ValidationReport, error)`
  wrapping resolve → `CollectSnapshot` → `ValidateState` → translation to a
  provider-owned report struct. Distinguishes readiness failure (SDK
  `ErrCodeInvalidRequest` from the pre-flight) from infrastructure errors.
- `provider/pkg/provider/validationrun.go` — the `infer.CustomResource`
  implementation; input validation reuses `validateArgs` helpers.
- Kubeconfig handling: unlike ClusterStack (which hands kubeconfig to the
  Kubernetes provider), ValidationRun must materialize a kubeconfig for the
  SDK's client-go usage — accept contents, write to a temp file when the SDK
  requires a path, honor ambient config when unset.
- Schema + SDK regeneration per the usual flow; examples get one optional
  ValidationRun block (kind example: expect `readiness` to pass and
  GPU checks to skip/fail — document that honestly).

## Testing strategy

- Adapter-level: readiness-failure classification and report translation are
  unit-testable against a fake snapshot (the SDK accepts a pre-supplied
  `Snapshot`, so no cluster is needed to test `ValidateState` wiring — needs
  verification of how much runs offline vs. requires Jobs).
- Resource-level: mock-based lifecycle tests (strict vs non-strict outcome
  mapping, trigger replacement).
- Live: the kind example plus the GPU test-rig runbook already exercised the
  underlying flow end-to-end via the CLI; a ValidationRun on the same rig is
  the acceptance test.

## Open questions

1. **Performance phase cost control** — require an explicit
   `phases: [..., performance]` *and* document expected runtime/hardware, or
   add a second confirmation knob? Current lean: explicit phase opt-in is
   enough.
2. **Snapshot reuse** — expose the snapshot as an output (or separate
   `Snapshot` resource) so multiple ValidationRuns / `aicr diff` drift checks
   can share one? Defer; single-shot covers the core need.
3. **Evidence/attestation** — the SDK can emit signed recipe-evidence
   bundles (`EmitRecipeEvidence`). Natural v2 feature for compliance flows;
   omit from v1.
4. **CTRF report output** — expose the merged CTRF JSON as an output for CI
   consumption? Cheap to add; size may argue for making it opt-in.
5. **Timeout defaults** — 30 min default covers deployment+conformance on
   small clusters; performance likely needs its own budget.

## Out of scope

- Running validation inside `ClusterStack` (rejected — failure semantics).
- Policy-pack enforcement (separate, org-level concern).
- Drift detection (`aicr diff`) — possible future `Snapshot`/`DriftCheck`
  resources; not needed for v1.
