# Design: `nvidia-aicr:index:ValidationRun`

Status: **draft, rev 2** — revised after review (2026-08-10). Informed by the
v0.18.0 SDK migration and the live EKS/H100-recipe test campaign
(July–August 2026).

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

Why not a policy pack: not impossibility — unsuitability. A stack validation
policy is arbitrary code running at the end of an update with outputs
available, so it *could* reach the cluster and run checks. But it would gain
nothing and lose a lot:

- **No stronger gating.** Stack policies run after provisioning; a mandatory
  violation fails the update but the cluster is already deployed — identical
  post-facto semantics to a `strict: true` ValidationRun.
- **No cadence control.** Policies run on every preview and update of every
  bound stack, with no equivalent of `triggers`; a ten-minute job-launching
  step taxes every `pulumi up` org-wide, and must self-skip at preview
  (unknown outputs, cluster may not exist) anyway.
- **Side effects from an observer.** A "policy" that deploys privileged Jobs
  into the cluster breaks CrossGuard's effectively side-effect-free contract,
  and depends on fragile access to the secret kubeconfig output.
- **No durable record or independent retry.** Policies emit violations, not
  structured per-check outputs in state; re-running one means another full
  update.

The two mechanisms compose instead: the policy pack's proper role is
*enforcing* validation org-wide — "every stack containing a ClusterStack must
contain a strict ValidationRun" / "ValidationRun.status must be `passed`" —
by reading this resource's outputs at end of update. Policy enforces that
validation happened; the resource implements it.

A separate resource gives us: validation recorded per-deployment in state and
the Console; independent retry (`pulumi up --replace` on the ValidationRun
without touching the cluster); opt-in cost; and a clean strict-mode choice.

## SDK surface (verified, v0.18.0)

The facade `github.com/NVIDIA/aicr/pkg/client/v1` already exposes everything
needed, on the same client the adapter uses for resolve/bundle:

- `CollectSnapshot(ctx, *AgentConfig) (*Snapshot, error)` — deploys a
  short-lived agent Job (privileged by default, temporary cluster-admin
  binding, self-cleaning) and captures cluster state. `AgentConfig.Kubeconfig`
  is a **path** (empty = in-cluster) — contents-style input must be
  materialized to a file. `Namespace`, `Image`, and `ServiceAccountName` are
  **required** — the provider must ship pinned defaults for the image and
  service-account name. `AgentConfig` also carries `NodeSelector`,
  `Tolerations`, `RequireGPU`, `ImagePullSecrets`, `Timeout`, `Cleanup`.
- `ValidateState(ctx, *RecipeResult, *Snapshot, ...) ([]*PhaseResult, error)`
  — runs the recipe's validation phases; validator Jobs are deployed to the
  cluster. **Caution: the SDK default runs all three phases, including
  performance.** The adapter must always pass `WithValidationPhases`
  explicitly so a wiring bug can't silently launch NCCL benchmarks. Options
  cover the knobs we need: `WithValidationNamespace` / `RunID` / `Cleanup` /
  `Tolerations` / `NodeSelector` / `Kubeconfig` / `Timeout` /
  `ImagePullSecrets` / `ImageRegistryOverride` / `ImageTagOverride` /
  `FailFast`, and `WithValidationNoCluster(true)` for unit tests (no
  Kubernetes resources created; every check reports skipped).
- `PhaseResult` is per-*phase*, not per-validator: `{Phase, Status, Duration,
  Summary, RawReport, Report}`. Per-validator detail lives in the CTRF report
  — the adapter parses it (via `Report` / `RawReport`) to build the
  `phaseResults` output. `RawReport` also makes exposing the merged CTRF JSON
  essentially free.
- **Error-code caveat**: readiness-check failures surface as
  `ErrCodeInvalidRequest` — but so do nil/closed client and
  foreign-`RecipeResult` programming errors. Classification by code alone
  would misreport a provider bug as "cluster doesn't match recipe." See
  verification items.

The ownership rule applies: the `RecipeResult` passed to `ValidateState` must
come from the same `Client` — so the resource resolves the recipe itself
(same criteria as ClusterStack) rather than accepting a foreign recipe
object. Extraction from ClusterStack can never skip re-resolution; it only
determines where the criteria *values* come from (see "Criteria wiring").

## Observed behavior to design around (from the live runs)

- **Readiness pre-flight**: before spending money on validator Jobs, the SDK
  checks recipe constraints against the snapshot (k8s version, node OS, GPU
  model). A mismatch aborts the run with `ErrCodeInvalidRequest` — e.g.
  `OS.release.ID expected ubuntu, got amzn`. This is a *distinct outcome*:
  "cluster doesn't match the recipe," not "checks failed" and not "error."
  The resource must surface it as such.
- **Scheduling**: GPU nodes in real clusters are almost always tainted
  (`nvidia.com/gpu:NoSchedule` on EKS GPU node groups, GKE GPU pools, GPU
  Operator defaults). Agent and validator Jobs without a matching toleration
  sit in `Pending` until the run times out — a validation failure with
  nothing wrong on the cluster. The resource must expose `tolerations` and
  `nodeSelector`; on tainted node groups they are required in practice, not
  nice-to-haves.
- **Runtime**: deployment + conformance ≈ 10 minutes on a 1-node cluster
  (8m21s + 1m36s observed). Performance phase is materially longer and
  requires recipe-matched GPU hardware. Timeouts must be generous and
  configurable; phases must be selectable.
- **Result shape**: per-validator `{name, phase, status: passed|failed|
  skipped|other, message}`. "other" occurs (e.g. a validator pod cleanup
  race) and must not be collapsed into pass or fail — it gets its own count
  and an explicit place in the rollup rule (below).
- **Cluster access**: everything runs through kubeconfig. Same input
  conventions as ClusterStack (contents vs path vs ambient, context).

## Proposed resource

A **custom resource** (`infer.CustomResource`, not a component) — it has real
create-time work and outputs, but creates no children.

```yaml
type: nvidia-aicr:index:ValidationRun
inputs:
  # Recipe criteria — one object, Input-typed. Unlike ClusterStack (which
  # resolves at plan time and therefore needs plain strings), ValidationRun
  # resolves only at create time, so criteria may be wired from outputs.
  # Canonical wiring: criteria: ${stack.criteria} (see below). Standalone
  # use (cluster deployed by another stack or the CLI) constructs it inline.
  criteria:                  # Input[RecipeCriteria], required
    accelerator: h100        # required
    service: eks             # required
    intent: training         # required
    os: ubuntu               # optional; same unset-is-OS-agnostic semantics
    platform: kubeflow       # optional
    nodes: 2                 # optional
  version: <Input[string]>   # optional recipe pin. Wire from
                             # stack.recipeVersion to validate exactly the
                             # deployed recipe, not whatever the criteria
                             # resolve to after a provider upgrade.

  # Cluster access — identical to ClusterStack. kubeconfig is marked secret
  # in the schema.
  kubeconfig: <Input[string]>
  kubeconfigPath: <string>
  context: <string>

  # Scheduling — required in practice on tainted GPU node groups.
  tolerations:               # k8s toleration shape, applied to agent and
    - key: nvidia.com/gpu    # validator Jobs
      operator: Exists
      effect: NoSchedule
  nodeSelector: {}           # optional

  # Images — defaults are pinned by the provider (AgentConfig requires
  # Image/ServiceAccountName to be set); overrides for air-gapped registries.
  imageRegistry: <string>    # optional registry override
  imagePullSecrets: []       # optional

  # What to run.
  phases: [deployment, conformance]   # default; "performance" opt-in
  strict: false              # default: checks failing does NOT fail the resource
  requireGpu: true           # snapshot agent lands on a GPU node, fails loudly
                             # if none. GPU-less clusters (kind) must set false.
  namespace: aicr-validation # namespace for agent/validator Jobs
  timeoutMinutes: 30
  includeCtrfReport: false   # opt-in: emit merged CTRF JSON as an output

  # Re-run control (command.local.Command convention): any change to
  # triggers replaces the resource, re-running validation.
  triggers: [<Input[any]>]
outputs:
  status: passed | failed | readiness-failed   # rollup, defined below
  readinessMessage: ""       # populated iff status == readiness-failed
  phaseResults:              # structured, per validator (parsed from CTRF)
    - { name: check-nvidia-smi, phase: deployment, status: passed, message: "" }
    - { name: dra-support, phase: conformance, status: failed, message: "..." }
  passed: 9
  failed: 3
  skipped: 1
  other: 0
  runId: "20260804-140434-..."
  completedAt: "..."
  ctrfReport: <json string>  # populated iff includeCtrfReport
```

**Rollup rule** (explicit): any `failed` check → `failed`; readiness
pre-flight mismatch → `readiness-failed`; otherwise `passed`. `other` never
flips the rollup but is always visible in its own count and in
`phaseResults`. Infrastructure errors (agent deploy failure, timeout,
unreachable cluster) have **no** status value: they fail Create in both
modes — "the run couldn't happen" is an error, not a result.

### Lifecycle semantics

| Operation | Behavior |
|---|---|
| Create | Resolve recipe → snapshot → validate → set outputs. With `strict: false` (default), the resource **succeeds** even when checks fail — results are data. With `strict: true`, any failed check (or readiness failure) fails the resource — **but must return partial state alongside the error** so `phaseResults`/`status` are still recorded; a strict failure that records nothing would defeat the durable-record rationale of this design. |
| Preview | No-op; all outputs unknown. Validation never runs at preview. |
| Any input change | Replace (delete is free) → fresh run. Not only `triggers`: replace-on-any-diff is predictable, and e.g. a `strict` flip legitimately warrants a re-run since it changes the rollup contract. |
| Delete | No-op. The SDK's agent and validator Jobs self-clean; nothing persists. |
| Refresh | No-op (results are a point-in-time record, not drifting state). |

Gating pattern for users who want deploy-blocked-on-validation: make
downstream resources `dependsOn` a `strict: true` ValidationRun. Teams that
want observation without gating use the default and read outputs.

### Criteria wiring — ClusterStack `criteria` output

To keep one source of truth between the stack and its validation, ClusterStack
gains a small additive output: `criteria` (a `RecipeCriteria` echoing its
resolved accelerator/service/intent/os/platform/nodes), alongside the
existing `recipeName`/`recipeVersion`. ValidationRun then wires directly:

```python
stack = aicr.ClusterStack("gpu", kubeconfig=cluster.kubeconfig_json, ...)

validation = aicr.ValidationRun("gpu-validation",
    criteria=stack.criteria,                # single source of truth — no drift
    version=stack.recipe_version,           # pin to the deployed recipe
    kubeconfig=cluster.kubeconfig_json,
    tolerations=[{"key": "nvidia.com/gpu", "operator": "Exists",
                  "effect": "NoSchedule"}],
    triggers=[stack.deployed_components],   # re-validate when the stack changes
)
pulumi.export("validation", validation.phase_results)
```

The output wiring makes criteria drift impossible (edit the stack's
accelerator and validation follows automatically) and creates the
`dependsOn` edge for free — no explicit `depends_on` needed.
`triggers=[stack.deployed_components]` gives the natural cadence: validation
re-runs when the deployed set changes, not on every `up`.

## Implementation sketch

- `provider/pkg/aicr/validate.go` — adapter extension: one function
  `Validate(ctx, criteria, kubeconfig, opts) (*ValidationReport, error)`
  wrapping resolve → `CollectSnapshot` → `ValidateState` → translation to a
  provider-owned report struct. Always passes `WithValidationPhases`
  explicitly (SDK default includes performance). Parses per-validator results
  out of the CTRF reports. Wires `tolerations`/`nodeSelector`/
  `imagePullSecrets`/`imageRegistry` into both `AgentConfig` and the
  `WithValidation*` options. Distinguishes readiness failure from
  infrastructure errors — and from other `ErrCodeInvalidRequest` causes (see
  verification items).
- `provider/pkg/provider/validationrun.go` — the `infer.CustomResource`
  implementation; input validation reuses `validateArgs` helpers. Strict-mode
  failures return partial state (outputs + error), not error alone.
- `provider/pkg/provider/clusterstack.go` — additive `criteria` output.
- Kubeconfig handling: unlike ClusterStack (which hands kubeconfig to the
  Kubernetes provider), ValidationRun must materialize a kubeconfig for the
  SDK's client-go usage — accept contents, write to a temp file (mode 0600,
  removed after the run) when the SDK requires a path, honor ambient config
  when unset. `kubeconfig` is a secret in the schema.
- `CLAUDE.md`: reword the "never use the SDK's own deployers" rule to scope
  it to *recipe component deployment*. ValidationRun deliberately lets the
  SDK deploy its own validation-harness Jobs — that is the validation
  mechanism, not stack deployment, and the rule should not read as violated.
- Schema + SDK regeneration per the usual flow; examples get one optional
  ValidationRun block. The kind example sets `requireGpu: false` (no GPU
  nodes) and documents honestly: readiness passes, GPU checks skip/fail.

## Testing strategy

- Adapter-level: readiness-failure classification and report translation are
  unit-testable against a fake snapshot; `WithValidationNoCluster(true)` runs
  the full `ValidateState` wiring with no cluster (every check reports
  skipped), covering phase selection and CTRF translation offline.
- Resource-level: mock-based lifecycle tests — strict vs non-strict outcome
  mapping, **partial-state persistence on strict failure**, replace-on-any-
  input-diff, rollup rule including `other`.
- Live: the kind example plus the GPU test-rig runbook already exercised the
  underlying flow end-to-end via the CLI; a ValidationRun on the same rig is
  the acceptance test (including scheduling onto tainted GPU nodes via the
  `tolerations` input).

## Verification items (before implementation)

1. **Partial state on failed Create** — confirm `pulumi-go-provider` v1.3.2
   `infer` supports returning state alongside an error from Create (init-
   error semantics). Strict mode depends on it.
2. **Readiness-failure discrimination** — `ErrCodeInvalidRequest` is shared
   with programming errors (nil/closed client, foreign recipe). Confirm the
   SDK exposes a typed error or stable sentinel for the readiness pre-flight;
   message-sniffing is a last resort and must be flagged as fragile.
3. **Cleanup on cancellation** — the agent is privileged with a temporary
   cluster-admin binding. Confirm the SDK reaps Jobs *and* the binding when
   the context is cancelled (Ctrl-C mid-`up`) or `ErrCodeTimeout` fires.
   Delete is a no-op, so nothing else ever reaps orphans; if the SDK's
   cleanup is happy-path only, the adapter needs a best-effort cleanup pass.
4. **Namespace ownership** — does the SDK create `aicr-validation` (and does
   cleanup remove it), or must it pre-exist? Determines whether the resource
   creates it, requires it, or documents it.

## Open questions

1. **Performance phase cost control** — require an explicit
   `phases: [..., performance]` *and* document expected runtime/hardware, or
   add a second confirmation knob? Current lean: explicit phase opt-in is
   enough, now that `tolerations`/`nodeSelector` make the jobs schedulable
   on real GPU node groups.
2. **Snapshot reuse** — expose the snapshot as an output (or separate
   `Snapshot` resource) so multiple ValidationRuns / `aicr diff` drift checks
   can share one? Defer; single-shot covers the core need.
3. **Evidence/attestation** — the SDK can emit signed recipe-evidence
   bundles (`EmitRecipeEvidence`). Natural v2 feature for compliance flows;
   omit from v1.
4. **Timeout defaults** — 30 min default covers deployment+conformance on
   small clusters; performance likely needs its own budget (possibly a
   per-phase timeout once the performance phase sees real use).

Resolved since rev 1: CTRF report output is opt-in via `includeCtrfReport`
(the SDK hands us `RawReport` anyway, so it is nearly free; opt-in keeps
state size in check).

## Out of scope

- Running validation inside `ClusterStack` (rejected — failure semantics).
- Policy-pack enforcement (separate, org-level concern).
- Drift detection (`aicr diff`) — possible future `Snapshot`/`DriftCheck`
  resources; not needed for v1.
