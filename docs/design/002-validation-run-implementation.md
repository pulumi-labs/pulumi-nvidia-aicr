# Design: `nvidia-aicr:index:ValidationRun` — implementation

Status: **implementation-ready** (2026-08-10). Refines
[001-validation-run.md](001-validation-run.md) (rev 2, approved). All four
verification items from 001 are resolved below against real source:
pulumi-go-provider v1.3.2 and AICR SDK v0.18.0 (module cache paths cited as
`pgp/…` = `github.com/pulumi/pulumi-go-provider@v1.3.2`, `aicr/…` =
`github.com/NVIDIA/aicr@v0.18.0`).

## Requirement

Add a custom resource `nvidia-aicr:index:ValidationRun` that runs an AICR
recipe's empirical validation (snapshot + deployment/conformance/performance
checks) against a live cluster at create time, records structured per-check
results in Pulumi state, and optionally (strict mode) fails the update while
still persisting results. Add an additive `criteria` output on `ClusterStack`
so validation wires from a single source of truth. Deployment of recipe
components stays untouched; the SDK's validation harness (agent + validator
Jobs) is deliberately allowed to deploy its own short-lived Jobs — that is the
validation mechanism, not stack deployment. `CLAUDE.md`'s "never use the SDK's
own deployers" rule is reworded to scope it to *recipe component deployment*.

### Verification findings (resolves 001 §"Verification items")

**1. Partial state on failed Create — CONFIRMED, with a consequence.**
`infer.ResourceInitFailedError` (`pgp/infer/errors.go:21–58`) is the
mechanism: return it from Create alongside a populated state and the framework
checkpoints that state (`errors.As` dispatch in `pgp/infer/resource.go:1271`
for Create, `:1370` Read, `:1440` Update); the resource is considered created
with init errors recorded. **Consequence:** per the documented semantics
(`errors.go:23–26`), "the next call will be Update with the new state" — the
engine forces an update step on a resource carrying init errors even when
inputs have not changed. `ValidationRun` must therefore implement
`CustomUpdate` (an unimplemented Update would fail the next `pulumi up` after
a strict failure). Second-order consequence: with `CustomUpdate` implemented,
infer's default diff no longer replaces on every change — it replaces only
properties whose schema sets `ReplaceOnChanges` (`pgp/infer/resource.go:
1132–1151`, note `:1147–1149` "No update => every change is a replace"), and
that lookup keys top-level property names only. We implement an explicit
`Diff` (below) as the single authoritative replace-on-any-change mechanism.

**2. Readiness-failure discrimination — NO typed sentinel; stable message
prefixes.** The readiness pre-flight is `checkReadiness`
(`aicr/pkg/validator/validator.go:50–75`), invoked from `ValidatePhases`
*before* the `NoCluster` short-circuit (`validator.go:209` vs `:219–221`). It
returns a generic `*errors.StructuredError` with `ErrCodeInvalidRequest` and
one of exactly two message prefixes:
`"readiness check failed: <name> expected <v>, got <actual>"` (`:68–69`) and
`"readiness check could not evaluate: <name>"` (`:62–65`, this one carries
`Context{"constraint","expected"}`). `pkg/errors` has no dedicated code or
sentinel (`aicr/pkg/errors/errors.go:26–49`; `StructuredError.Is` matches by
code only, `:82–91`). `ErrCodeInvalidRequest` is shared with nil/closed
client, foreign `RecipeResult`, invalid phase values
(`aicr/pkg/client/v1/aicr.go:1260–1285, 1341–1353`), and dependencyAffinity
pre-flight failures (`validator.go:375–378`). **Design:** classify via
`errors.As(*StructuredError)` + `Code == ErrCodeInvalidRequest` + prefix match
on `se.Message` (the unformatted field — not `Error()`, which prepends
`[INVALID_REQUEST]`) against the two pinned prefixes. Both prefixes map to
`readiness-failed` (both mean "recipe constraints cannot be affirmed against
this cluster"). Fragility is mitigated by a regression test that drives the
real `checkReadiness` offline (see Test plan) so an SDK bump that changes the
strings breaks CI, not production classification.

**3. Cleanup on cancellation/timeout — mostly safe; one gap.**
- *Snapshot agent:* `deployAndWaitForResult` defers `deployer.Cleanup` with a
  **fresh** `context.Background()` + `K8sCleanupTimeout`
  (`aicr/pkg/snapshotter/agent.go:185–197`), which deletes the Job, SA, Role,
  RoleBinding, ClusterRole and ClusterRoleBinding
  (`aicr/pkg/k8s/agent/deployer.go:101–121`) when `Cleanup: true`. Agent
  resources are reaped on Ctrl-C and on `ErrCodeTimeout`. Correction to 001's
  characterization: the agent binds a *scoped* ClusterRole
  `"aicr-node-reader"` (`aicr/pkg/k8s/agent/types.go:23`), **not**
  cluster-admin. The agent's result ConfigMap (`aicr-snapshot`) is *not* in
  the cleanup list and persists in the namespace.
- *Validator:* the per-run ServiceAccount + **cluster-admin**
  ClusterRoleBinding `aicr-validator-<runID>`
  (`aicr/pkg/validator/job/rbac.go:36–51, 83`) and the data/CTRF ConfigMaps
  are cleaned by `deferClusterCleanup` with a **fresh** context
  (`aicr/pkg/validator/validator.go:165–181`) — reaped on cancellation. The
  security-sensitive binding is therefore safe. **Gap:** the per-check
  validator Job cleanup uses the *parent* ctx (`deployer.CleanupJob(ctx)`,
  `validator.go:453–461`) and the phase loop exits early on `ctx.Done()`
  (`:255–258, :386–390`) — a cancellation mid-check can orphan the in-flight
  Job (and a Pod potentially holding a GPU) in the validation namespace.
  **Design:** the adapter adds a best-effort orphan sweep: when
  `ValidateState` returns with the parent ctx canceled/expired, delete Jobs in
  the validation namespace matching the run's `labels.RunID` label
  (`aicr/pkg/validator/labels`), using a fresh 30 s context and the SDK's
  `k8sclient.BuildKubeClient`. Failure to sweep is logged, never fatal.
- Delete remains a no-op: nothing else persists that the SDK does not reap.

**4. Namespace ownership — SDK creates it; nothing removes it.** Both the
agent deployer (`ensureNamespace`, `aicr/pkg/k8s/agent/rbac.go:41–79`, called
from `Deploy` step 1, `deployer.go:52`) and the validator (`ensureNamespace`
via server-side apply, `aicr/pkg/validator/validator.go:676–690`, called from
`prepareCluster:135`) create the namespace if missing (and label it
`app.kubernetes.io/managed-by=aicr`). Neither cleanup path deletes it (absent
from the agent Cleanup task list; `deferClusterCleanup` removes only
RBAC/ConfigMaps). **Design:** ValidationRun neither creates nor requires the
namespace; docs state that the SDK creates `aicr-validation` on first run,
that it (plus a leftover `aicr-snapshot` ConfigMap) persists afterward, and
that `Delete` deliberately leaves it. The kubeconfig identity needs RBAC to
create/patch Namespaces, ClusterRoles, and ClusterRoleBindings.

### Deviations from 001 (forced by findings)

1. **`recipeDataVersion` is an assertion, not a resolution pin.** The SDK rejects
   `PinnedName`/`PinnedVersion` with `ErrCodeUnavailable`
   (`aicr/pkg/client/v1/types.go:243–250`), so a pinned re-resolution is
   impossible in v0.18.0. Semantics: when `version` is set and does not equal
   the provider's embedded recipe-data version (`aicr.sdkModuleVersion()`),
   Create fails fast with a friendly mismatch message *before any cluster
   work*. Wiring `version: stack.recipeVersion` still delivers 001's drift
   guard: a provider upgrade between deploy and validate is caught loudly.
2. **`CustomUpdate` must be implemented** (finding 1). User-visible effect
   beyond 001's table: after a strict-mode failure, validation re-runs on
   every subsequent `pulumi up` (init-error repair path) until it passes —
   documented as intended strict-mode behavior.
3. **Readiness classification is message-prefix matching** (finding 2) — the
   "last resort" 001 flagged; guarded by a pinned-prefix regression test.
4. **Adapter orphan sweep for validator Jobs on cancellation** (finding 3).
5. **Agent privilege description corrected**: scoped `aicr-node-reader`
   ClusterRole for the agent; cluster-admin is the *validator* CRB (both
   reaped by the SDK, including on cancellation).
6. **Additive outputs** beyond 001's list: `recipeName`, `recipeVersion`
   (records exactly which recipe was validated).
7. **Preview shows empty result outputs**, not "unknown": infer custom
   resources return concrete preview state; we return the input echo with
   zero-valued results. Cosmetic only; noted in docs.

> **Naming note (2026-08-13):** this input ships as `recipeDataVersion`, not
> `version`. pulumi-go-provider's infer diff path silently strips any input
> property named `version` (infer/resource.go, "We ignore version input from
> the engine"), which made every live diff decode new-nil vs old-set and
> report a perpetual spurious replace. Found on the EKS rig; guarded by the
> wire-level lifecycle tests in `validationrun_wire_test.go`. Never name a
> custom-resource input `version`.


### Open questions (carried, none blocking)

- Per-phase timeout budget once the performance phase sees real use (001 Q4).
- Concurrent ValidationRuns against one cluster share the agent's fixed RBAC
  names (SDK limitation; validator RBAC is per-runID and safe). Documented,
  not solved here.

## Public API

### Schema impact

New resource `nvidia-aicr:index:ValidationRun`; new shared object types
`nvidia-aicr:index:RecipeCriteria`, `nvidia-aicr:index:Toleration`,
`nvidia-aicr:index:CheckResult`; new `criteria` output on
`nvidia-aicr:index:ClusterStack`. All additive. `make schema` must be re-run
and the committed `provider/cmd/pulumi-resource-nvidia-aicr/schema.json`
updated; CI diffs it. Multi-language SDKs regenerate via the usual flow.

### Shared criteria type (new, `provider/pkg/provider/validationrun.go`)

Used both as the ClusterStack output and the ValidationRun input — one schema
type, no drift. Plain Go types: ValidationRun is a custom resource whose
inputs resolve at create time, so output-wired values are fine (unknown at
preview only).

```go
// RecipeCriteria mirrors ClusterStack's recipe-selection inputs.
type RecipeCriteria struct {
	Accelerator string  `pulumi:"accelerator"`
	Service     string  `pulumi:"service"`
	Intent      string  `pulumi:"intent"`
	OS          *string `pulumi:"os,optional"`
	Platform    *string `pulumi:"platform,optional"`
	Nodes       *int    `pulumi:"nodes,optional"`
}
```

### ValidationRun inputs

```go
type ValidationRunArgs struct {
	// Recipe criteria; wire from a ClusterStack's `criteria` output or build inline.
	Criteria RecipeCriteria `pulumi:"criteria"`
	// Optional recipe-data version ASSERTION (not a pin — see deviations).
	// Property name recipeDataVersion: "version" is stripped by infer's diff path.
	// Wire from stack.recipeVersion; mismatch with this provider build fails Create.
	Version *string `pulumi:"version,optional"`

	// Cluster access — same conventions as ClusterStack, but plain *string
	// (custom resources resolve inputs at create time; outputs still wire).
	Kubeconfig     *string `pulumi:"kubeconfig,optional" provider:"secret"`
	KubeconfigPath *string `pulumi:"kubeconfigPath,optional"`
	Context        *string `pulumi:"context,optional"`

	// Scheduling — required in practice on tainted GPU node groups.
	Tolerations  []Toleration      `pulumi:"tolerations,optional"`
	NodeSelector map[string]string `pulumi:"nodeSelector,optional"`

	// Images — provider pins agent/validator defaults; overrides for air-gap.
	ImageRegistry    *string  `pulumi:"imageRegistry,optional"`
	ImagePullSecrets []string `pulumi:"imagePullSecrets,optional"`

	// What to run. Default (nil): ["deployment", "conformance"].
	// "performance" is explicit opt-in. Defaulted in code, not Annotate.
	Phases []string `pulumi:"phases,optional"`

	Strict            *bool   `pulumi:"strict,optional"`            // default false
	RequireGpu        *bool   `pulumi:"requireGpu,optional"`        // default true; kind must set false
	Namespace         *string `pulumi:"namespace,optional"`         // default "aicr-validation"
	TimeoutMinutes    *int    `pulumi:"timeoutMinutes,optional"`    // default 30
	IncludeCtrfReport *bool   `pulumi:"includeCtrfReport,optional"` // default false

	// Any change replaces the resource (fresh run) — command.local convention.
	Triggers []interface{} `pulumi:"triggers,optional"`
}

// Toleration is the standard k8s toleration shape, provider-owned so the
// schema does not depend on the kubernetes package's types.
type Toleration struct {
	Key               *string `pulumi:"key,optional"`
	Operator          *string `pulumi:"operator,optional"` // "Exists" | "Equal"
	Value             *string `pulumi:"value,optional"`
	Effect            *string `pulumi:"effect,optional"`   // NoSchedule | PreferNoSchedule | NoExecute
	TolerationSeconds *int    `pulumi:"tolerationSeconds,optional"`
}
```

Notes:
- `provider:"secret"` is the supported tag namespace for secrets
  (`pgp/internal/introspect/introspect.go:173–184`; marking secret in the
  `pulumi` namespace is a hard error).
- No `provider:"replaceOnChanges"` tags: replace semantics are owned entirely
  by the explicit `Diff` (finding 1) so nested-property diffs (e.g.
  `tolerations[0].key`) can't slip through as in-place updates.
- Defaults for `Strict`/`RequireGpu`/`Namespace`/`TimeoutMinutes`/
  `IncludeCtrfReport` set via `Annotate` `SetDefault`; `Phases` defaulted in
  code (nil → deployment+conformance) and documented in `Describe`.

### ValidationRun state (outputs)

```go
type ValidationRunState struct {
	ValidationRunArgs // input echo — required for Diff (finding 1) and state inspection

	// Rollup: "passed" | "failed" | "readiness-failed". Infrastructure
	// errors have NO status value — they fail Create/Update outright.
	Status           string        `pulumi:"status"`
	ReadinessMessage string        `pulumi:"readinessMessage"` // "" unless readiness-failed
	PhaseResults     []CheckResult `pulumi:"phaseResults"`
	Passed           int           `pulumi:"passed"`
	Failed           int           `pulumi:"failed"`
	Skipped          int           `pulumi:"skipped"`
	Other            int           `pulumi:"other"`
	RunID            string        `pulumi:"runId"`
	CompletedAt      string        `pulumi:"completedAt"` // RFC 3339
	RecipeName       string        `pulumi:"recipeName"`
	RecipeVersion    string        `pulumi:"recipeVersion"`
	CtrfReport       *string       `pulumi:"ctrfReport,optional"` // set iff includeCtrfReport
}

// CheckResult is one validator's outcome, parsed from the CTRF report.
type CheckResult struct {
	Name    string `pulumi:"name"`
	Phase   string `pulumi:"phase"`
	Status  string `pulumi:"status"` // passed | failed | skipped | pending | other
	Message string `pulumi:"message"`
}
```

The anonymous `ValidationRunArgs` embed flattens the input properties into
state (standard infer pattern; the doc example in `pgp/infer/errors.go:39–48`
persists inputs in state for exactly this reason). `kubeconfig` stays secret
in state via its tag.

Rollup rule (unchanged from 001): any `failed` check → `failed`; readiness
pre-flight mismatch → `readiness-failed`; otherwise `passed`. `other` never
flips the rollup but is always counted and listed. Note the deliberate
divergence from the SDK's `ctrf.IsFailingStatus` (which treats `other` as
blocking for *fail-fast*, `aicr/pkg/validator/ctrf/types.go:51–53`): 001
explicitly decided `other` is visible-but-not-fatal at the resource level.

### ClusterStack additive output

```go
// added to ClusterStack state (provider/pkg/provider/clusterstack.go)
Criteria RecipeCriteria `pulumi:"criteria"`
```

Populated with the **canonicalized** values actually used for resolution
(post-`canonical`/`canonicalOr`, `Nodes` as provided), so wiring it into
ValidationRun re-resolves identical criteria. Also added to the
`RegisterResourceOutputs` map as a `pulumi.Map` (omitting unset optionals),
mirroring how `recipeName` is registered today. `Annotate` gains a
description; `RecipeCriteria` gets its own `Annotate` (and the compile-time
`infer.Annotated` checks are extended).

### Implementation surface (file layout & signatures)

**`provider/pkg/aicr/validate.go`** (adapter; imports the facade only —
`pkg/client/v1`, plus `pkg/validator/ctrf` for report types,
`pkg/validator/labels` + `pkg/k8s/client` for the orphan sweep,
`k8s.io/api/core/v1` for tolerations):

```go
// ValidateOptions carries everything beyond criteria that a run needs.
type ValidateOptions struct {
	KubeconfigPath   string // "" = ambient discovery (KUBECONFIG / ~/.kube/config / in-cluster)
	Namespace        string
	Tolerations      []corev1.Toleration
	NodeSelector     map[string]string
	ImageRegistry    string   // "" = default registry
	ImagePullSecrets []string
	Phases           []string // REQUIRED non-empty, canonical values; adapter errors on empty
	RequireGPU       bool
	NoCluster        bool     // test seam → WithValidationNoCluster(true)
	RunID            string   // "" = adapter generates; always passed to WithValidationRunID
}

type Outcome string

const (
	OutcomePassed          Outcome = "passed"
	OutcomeFailed          Outcome = "failed"
	OutcomeReadinessFailed Outcome = "readiness-failed"
)

type CheckOutcome struct{ Name, Phase, Status, Message string }

type ValidationReport struct {
	Outcome          Outcome
	ReadinessMessage string
	Checks           []CheckOutcome
	Passed, Failed, Skipped, Other int
	RunID            string
	RecipeName       string
	RecipeVersion    string
	CompletedAt      time.Time
	CTRFReport       string // merged CTRF JSON (always captured; resource decides exposure)
}

// Validate resolves the recipe for criteria on a fresh client, collects a
// snapshot, runs the selected validation phases, and translates the results.
// Contract: (report, nil) for passed / failed / readiness-failed;
// (nil, err) only for infrastructure errors (client init, resolve, snapshot
// deploy/timeout, non-readiness ValidateState errors).
func Validate(ctx context.Context, criteria Criteria, opts ValidateOptions) (*ValidationReport, error)

// isReadinessError classifies err per finding 2. Exported to tests via
// an internal_test; prefixes are package constants.
func isReadinessError(err error) (msg string, ok bool)

// buildReport translates facade PhaseResults (typed *ctrf.Report) into
// checks + counts + merged CTRF JSON. Pure; unit-tested directly.
func buildReport(results []*aicrclient.PhaseResult) (checks []CheckOutcome, counts [4]int, mergedCTRF string, err error)

// agentImage derives the pinned snapshot-agent image:
// "<registry-or-ghcr.io>/nvidia/aicr:" + sdkModuleVersion(). When the module
// version is unresolvable ("embedded") it FAILS CLOSED rather than using the
// mutable :latest tag (security review); the PULUMI_NVIDIA_AICR_AGENT_IMAGE
// env var supplies a verbatim image reference for dev builds. Tag scheme
// matches the CLI's (aicr/pkg/cli/root.go:40–55).
func agentImage(registryOverride string) (string, error)

// sweepValidatorJobs best-effort deletes Jobs labeled with runID in the
// validation namespace after a canceled/expired run (finding 3). Fresh 30s
// context; errors are returned for logging only.
func sweepValidatorJobs(kubeconfigPath, namespace, runID string) error
```

`Validate` internals, in order: `NewClient(EmbeddedSource())` (client kept
open across resolve→validate — `ValidateState` requires the same-client
`RecipeResult`, `aicr/pkg/client/v1/aicr.go:1221–1225`; `aicr.Resolve` can't
be reused because it closes its client) → `ResolveRecipe` → `CollectSnapshot`
with `AgentConfig{Kubeconfig: opts.KubeconfigPath, Namespace, Image:
agentImage(...), ServiceAccountName: "aicr-agent", JobName: "aicr-snapshot-" +
runID, ImagePullSecrets, NodeSelector, Tolerations, Privileged: true,
RequireGPU: opts.RequireGPU, Cleanup: true, Timeout: 10 * time.Minute}`
(`Namespace`/`Image`/`ServiceAccountName` are required per
`aicr/pkg/client/v1/aicr.go:1159–1161`) → `ValidateState` with **always**
`WithValidationPhases(...)` (SDK default runs all three phases incl.
performance — `aicr.go:1216–1219`), plus `WithValidationNamespace`,
`WithValidationRunID(runID)`, `WithValidationCleanup(true)`,
`WithValidationKubeconfig`, `WithValidationTolerations`,
`WithValidationNodeSelector`, `WithValidationImagePullSecrets`,
`WithValidationImageRegistryOverride`, and `WithValidationNoCluster` when
opts.NoCluster (all confirmed present, `aicr/pkg/client/v1/options.go:
170–314`). No `WithValidationTimeout`: the resource's ctx deadline governs
(the facade honors the smaller of parent deadline and its 75 m default cap,
`aicr.go:1305–1333`). On `ValidateState` error: readiness → report with
`OutcomeReadinessFailed`; ctx canceled/expired → run `sweepValidatorJobs`,
then return the error; anything else → return the error.

**`provider/pkg/provider/validationrun.go`** (resource):

```go
type ValidationRun struct{}

func (*ValidationRun) Create(ctx context.Context, req infer.CreateRequest[ValidationRunArgs]) (infer.CreateResponse[ValidationRunState], error)
func (*ValidationRun) Diff(ctx context.Context, req infer.DiffRequest[ValidationRunArgs, ValidationRunState]) (infer.DiffResponse, error)
func (*ValidationRun) Update(ctx context.Context, req infer.UpdateRequest[ValidationRunArgs, ValidationRunState]) (infer.UpdateResponse[ValidationRunState], error)
func (*ValidationRun) Delete(ctx context.Context, req infer.DeleteRequest[ValidationRunState]) (infer.DeleteResponse, error)

// runValidation is the shared Create/Update body: validate → materialize
// kubeconfig → aicr.Validate → translate to state (+ strict-mode error).
func runValidation(ctx context.Context, name string, args ValidationRunArgs) (id string, state ValidationRunState, err error)

// validateValidationRunArgs mirrors validateArgs' style: criteria checks via
// the shared helper below, phases allowlist, kubeconfig exclusivity,
// timeoutMinutes > 0, version-assertion check.
func validateValidationRunArgs(args *ValidationRunArgs) error

// materializeKubeconfig returns a kubeconfig PATH for the SDK plus a cleanup
// func. Contents → temp file (0600); path w/o context → passthrough (no temp
// file); `context` set → load (contents/path/ambient), set current-context,
// write temp file; nothing set → "" (ambient discovery).
func materializeKubeconfig(kubeconfig, kubeconfigPath, kubeContext *string) (path string, cleanup func(), err error)

// validateFn is a package seam over aicr.Validate for lifecycle tests.
var validateFn = aicr.Validate
```

**`provider/pkg/provider/clusterstack.go`**: extract the criteria-value
validation from `validateArgs` into a shared
`validateCriteria(accelerator, service, intent string, os, platform *string,
nodes *int) error` (allowlists + `validateCompatibility` + nodes bounds), so
ClusterStack and ValidationRun produce byte-identical friendly errors. Add the
`Criteria` output.

**`provider/pkg/provider/provider.go`**: register via
`Resources: []infer.InferredResource{infer.Resource(&ValidationRun{})}`
(`infer.Resource` takes a receiver instance in v1.3.2,
`pgp/infer/resource.go:934–936`). Token: `nvidia-aicr:index:ValidationRun`
via the existing `provider → index` module map.

**`CLAUDE.md`**: reword the deployers rule; add a short ValidationRun section
(namespace persistence, RBAC needs, strict-mode re-run behavior).

**Examples**: one optional ValidationRun block per example; kind sets
`requireGpu: false` and documents honestly (readiness passes, GPU checks
skip/fail).

## Behavior

### Plan time vs deploy time

- **Plan time (preview):** nothing runs. `Create`/`Update` with
  `req.DryRun == true` return immediately with the input echo and zero-valued
  result fields (see deviation 7). Input validation runs opportunistically at
  preview but skips required-field checks when `Criteria` is zero-valued
  (criteria wired from an unresolved output arrives as the zero struct at
  preview); full validation always re-runs at apply.
- **Deploy time (apply):** `runValidation`:
  1. `validateValidationRunArgs` (friendly errors before any SDK/cluster
     work): criteria via `validateCriteria`; phases ⊆ {deployment,
     conformance, performance} (case-insensitive, deduped, default
     `[deployment conformance]` when nil/empty); `kubeconfig` xor
     `kubeconfigPath`; `timeoutMinutes >= 1`; `version` assertion vs
     `aicr.SDKVersion()` (thin exported wrapper over `sdkModuleVersion`).
  2. `materializeKubeconfig` → path + deferred cleanup (temp file mode 0600
     via `os.CreateTemp`, removed in all paths including error/cancel).
  3. `ctx, cancel := context.WithTimeout(ctx, timeoutMinutes)`.
  4. `validateFn(ctx, criteria, opts)` with tolerations translated to
     `corev1.Toleration`.
  5. Translate report → state; `ID = req.Name + "-" + report.RunID`;
     `CompletedAt = time.Now().UTC().Format(time.RFC3339)`.

### Child resources

None. ValidationRun is a leaf custom resource. The SDK's agent Job, validator
Jobs, per-run RBAC, ConfigMaps, and the `aicr-validation` namespace are
cluster-side ephemera owned by the SDK (namespace + `aicr-snapshot` ConfigMap
persist; see finding 4) — never Pulumi resources.

### Failure modes and strict semantics

| Run outcome | `strict: false` (default) | `strict: true` |
|---|---|---|
| all checks pass | success, `status: passed` | success, `status: passed` |
| ≥1 check failed | **success**, `status: failed`, counts populated | `infer.ResourceInitFailedError{Reasons: ["N validation check(s) failed: a, b, …"]}` **with full state** — results persist, update fails |
| readiness pre-flight mismatch | success, `status: readiness-failed`, `readinessMessage` set | init-failed error with full state, reason = readiness message |
| infrastructure error (client, resolve, agent deploy, timeout, unreachable cluster) | plain error, **no state** — "the run couldn't happen" is an error, not a result | same |
| `other`-status checks only | success, `status: passed`, `other` count > 0 | same (other never flips the rollup) |

After a strict failure, the engine's init-error semantics call `Update` on the
next `pulumi up` even with no input diff (finding 1); `Update` re-runs
validation via `runValidation` and returns fresh state (again with
`ResourceInitFailedError` if still failing) — strict validation retries every
update until it passes.

### Preview / diff / lifecycle semantics

| Operation | Behavior |
|---|---|
| Preview | No-op; input echo + empty results returned; validation never runs. |
| Diff | Explicit: compare each top-level input property of `req.Inputs` against the echo in `req.State` (`reflect.DeepEqual` per property); every changed property reported as `p.UpdateReplace`; `HasChanges` accordingly; `DeleteBeforeReplace: true` (delete is free). Any input change ⇒ replace ⇒ fresh run — including `strict` flips and `triggers`. |
| Update | Only reachable through init-error repair (no input diff). Re-runs validation. `DryRun` → echo current inputs, keep results. |
| Delete | Explicit no-op (`DeleteResponse{}`), with a comment: SDK self-cleans; namespace deliberately persists. |
| Read / Refresh | Not implemented — refresh keeps the recorded point-in-time results. |

Gating pattern (unchanged from 001): downstream resources `dependsOn` a
`strict: true` ValidationRun; observers use the default and read outputs.
`triggers: [stack.deployedComponents]` gives re-validation cadence.

### CTRF → `phaseResults` translation

`ValidateState` returns one facade `PhaseResult` per phase with a typed
`*ctrf.Report` attached (`aicr/pkg/client/v1/types.go:59–66`; population in
`translate.go:128–156`). `buildReport` uses the typed `Report` directly (no
JSON re-parse of `RawReport`):

- per check: `Report.Results.Tests[]` → `CheckOutcome{Name: t.Name, Phase:
  string(pr.Phase), Status: t.Status, Message: t.Message}`
  (`aicr/pkg/validator/ctrf/types.go:124–144`);
- counts: sum of `Report.Results.Summary.{Passed,Failed,Skipped,Other}`
  across phases (`Pending` folded into `Other` for the outputs — CTRF allows
  it but the validator never emits it in practice; folding keeps the output
  contract at four counts per 001);
- rollup from the summed counts per the rule above;
- merged CTRF: `ctrf.MergeReports(reports…)` → `json.Marshal` → stored in
  `ValidationReport.CTRFReport`; the resource copies it into
  `state.CtrfReport` only when `includeCtrfReport` (state-size control);
- a phase with a nil `Report` (defensive) contributes zero checks and is
  ignored for counts — flagged in logs, not fatal.

### Kubeconfig materialization

`AgentConfig.Kubeconfig` and `WithValidationKubeconfig` take a *path*
(`aicr/pkg/client/v1/aicr.go:1159`, options.go); empty means ambient
discovery via `k8sclient.GetKubeClient` (`aicr/pkg/snapshotter/agent.go:
288–302`, `aicr/pkg/validator/validator.go:117–130`). Resource behavior:

- `kubeconfig` (contents, secret): write to `os.CreateTemp` (0600 by
  default), pass the path, remove via deferred cleanup after the run —
  including error and cancellation paths. Never logged.
- `kubeconfigPath` without `context`: passed through verbatim; no temp file.
- `context` set (with either source or ambient): load via
  `clientcmd.LoadFromFile`/`clientcmd.Load`, set `CurrentContext`, validate
  the context exists (friendly error otherwise), serialize to a 0600 temp
  file. Required because the SDK offers no context knob.
- Nothing set: `""` → ambient.
- `kubeconfig` and `kubeconfigPath` together: rejected in validation (same
  message as ClusterStack).

## Alternatives considered

1. **Rely on infer's default "no Update ⇒ every change replaces" and skip
   `CustomUpdate`** — simplest replace-on-any-change. Rejected: strict mode's
   init-error recovery calls Update on the next `pulumi up` (finding 1); with
   no Update implemented that up fails with an unimplemented-method error,
   wedging the stack until a manual `--replace`. Update is mandatory, which
   in turn mandates the explicit Diff (default diff with Update present only
   replaces `ReplaceOnChanges`-tagged top-level properties, and nested paths
   like `tolerations[0].key` would degrade to in-place updates).
2. **`provider:"replaceOnChanges"` tags instead of a custom Diff** —
   declarative and schema-visible. Rejected as the authoritative mechanism
   for the nested-path gap above (`pgp/infer/resource.go:1141–1146` keys
   top-level `InputProperties` only); a custom Diff is deterministic for
   every property shape and directly unit-testable.
3. **Classify readiness by error code alone (`ErrCodeInvalidRequest`)** — no
   string matching. Rejected per finding 2: the code is shared with
   programming errors (nil/closed client, foreign recipe, invalid phase,
   dependencyAffinity failures); misclassifying a provider bug as "cluster
   doesn't match recipe" corrupts the resource's core signal. Prefix matching
   on the pinned SDK version with a CI tripwire is strictly safer.
4. **Honor `version` by pinned re-resolution** — rejected: unimplementable on
   v0.18.0 (`PinnedVersion` → `ErrCodeUnavailable`); assertion semantics
   deliver the same drift protection (deviation 1). Revisit if a later SDK
   implements pinning.
5. **Create the validation namespace as a Pulumi child resource** — would
   make Delete meaningful. Rejected: the SDK creates and labels it anyway
   (finding 4), Pulumi ownership would fight the SDK's SSA label writes and
   turn ValidationRun into a component for one inert namespace; 001's
   "delete is free" replace semantics depend on Delete being a no-op.

## Risks

- **Readiness message prefixes drift on SDK bump.** Mitigated by the pinned
  regression test (below) that exercises the real `checkReadiness` path; the
  SDK-bump checklist in CLAUDE.md gains "run adapter tests" (already
  standard). Worst case on silent drift: readiness mismatches surface as
  infrastructure errors (fail-closed — Create errors rather than recording a
  wrong status).
- **Init-error → forced Update interplay** is the least-trodden part of the
  engine contract. Mitigated with dedicated lifecycle tests (Update-after-
  strict-failure) and by keeping Update byte-identical to Create via
  `runValidation`.
- **Privilege surface**: the run creates a privileged agent (scoped
  ClusterRole) and per-run cluster-admin validator RBAC. All reaped by the
  SDK including on cancellation (finding 3); the one gap (in-flight validator
  Job) is swept by the adapter and is not privilege-bearing beyond its
  ServiceAccount, whose CRB the SDK deletes regardless.
- **State size**: CTRF reports can be large; mitigated by opt-in
  `includeCtrfReport` and per-check `Message` (not full stdout) in
  `phaseResults`.
- **Cost/runtime**: deployment+conformance ≈ 10 min observed; performance
  materially longer and hardware-bound. Mitigated by explicit phase opt-in,
  `timeoutMinutes`, and replace-only-on-input-change cadence via `triggers`.
- **Concurrent runs**: agent RBAC/Job names are per-config, not per-run
  (except our per-run JobName); two simultaneous ValidationRuns against one
  cluster may contend on the agent SA/ClusterRole. Documented limitation.

## Test plan

### Adapter unit tests (`provider/pkg/aicr/validate_test.go`)

1. **NoCluster full wiring**: `Validate(ctx, kindCriteria,
   ValidateOptions{NoCluster: true, Phases: ["deployment","conformance"],
   Namespace: "aicr-validation", …})` → no error; every check `skipped`;
   `Skipped == len(Checks) > 0`; `Passed == Failed == Other == 0`; Outcome
   `passed`; `RunID` non-empty; no `performance` phase appears in `Checks`
   (proves `WithValidationPhases` is always passed); `CTRFReport` parses as
   JSON and its summary matches the counts.
2. **Readiness classification — live SDK path (pinned-prefix tripwire)**:
   resolve a recipe carrying constraints on a real client, call
   `client.ValidateState` with a bare `&aicrclient.Snapshot{}` (nil internal
   → minimal snapshot) and `WithValidationNoCluster(true)`; readiness runs
   *before* the NoCluster short-circuit (`validator.go:209` vs `:219`), so
   the constraint evaluation fails offline. Assert `isReadinessError` returns
   `ok == true` and a non-empty message. This test breaks on any SDK bump
   that changes the prefixes.
3. **`isReadinessError` table**: `"readiness check failed: …"` → true;
   `"readiness check could not evaluate: …"` (with Context) → true;
   `ErrCodeInvalidRequest` + `"aicr client not initialized"` → false;
   `ErrCodeInternal` + readiness-looking message → false; wrapped chains
   (`fmt.Errorf("%w")` around the StructuredError) → still classified; plain
   `errors.New` → false.
4. **`buildReport` rollup incl. `other`** (fabricated `ctrf.Report`s):
   all-passed → `passed`; one failed among passed → `failed` with correct
   counts and the failing check's `Message` propagated; only `other` →
   `passed` with `Other == 1` (never flips); `skipped`+`passed` mix;
   `pending` folded into `Other`; two phases merge with per-check `Phase`
   set from the owning phase; nil `Report` on one phase → skipped without
   error; merged CTRF JSON summary equals summed counts.
5. **`agentImage`**: no override → `ghcr.io/nvidia/aicr:` + module version;
   version "embedded" → fails closed (no `:latest` fallback; security
   review), with `PULUMI_NVIDIA_AICR_AGENT_IMAGE` as the verbatim dev-build
   override, bypassing the registry override; registry override rewrites the
   registry and keeps repo+tag; error propagation through `Validate` is
   asserted.
6. **Option guards**: empty `Phases` → error (defense against the SDK's
   run-everything default); RunID passthrough vs generated.

### Resource lifecycle tests (`provider/pkg/provider/validationrun_test.go`, `validateFn` seam)

7. **Args validation** (negative): missing accelerator/service/intent (same
   messages as ClusterStack via `validateCriteria`); unsupported platform
   combo (`kubeflow`+`inference`); bad phase `"performence"` lists valid
   values; `kubeconfig`+`kubeconfigPath` together; `timeoutMinutes: 0`;
   `version: "v0.17.0"` vs embedded → friendly mismatch naming both
   versions; canonicalization (`" EKS "` accepted).
8. **Strict partial-state persistence**: fake returns `OutcomeFailed` →
   `Create` returns error satisfying
   `errors.As(&infer.ResourceInitFailedError{})` **and** a
   `CreateResponse` whose state carries `status: "failed"`, counts,
   `phaseResults`, `runId`; Reasons name the failing checks.
9. **Non-strict outcome mapping**: failed → nil error + `status: failed`;
   readiness-failed → nil error + `readinessMessage` set + zero counts;
   passed → `status: passed`; `includeCtrfReport` false → `CtrfReport` nil,
   true → populated.
10. **Infrastructure error**: fake returns `(nil, err)` → Create returns the
    error and no init-failed marker (plain failure, nothing persisted).
11. **Replace-on-any-diff**: for each top-level input property (criteria
    field, strict flip, a toleration's nested key, nodeSelector entry,
    triggers element) mutate it → `Diff` reports `HasChanges` with that
    property as `UpdateReplace`; identical inputs → no changes; secret-ness
    of kubeconfig does not affect diffing.
12. **Update = re-run**: `Update` with unchanged inputs (init-error repair
    path) invokes the fake again and returns fresh state; strict+still-
    failing → `ResourceInitFailedError` again with new state; `DryRun`
    Update does not invoke the fake.
13. **Preview**: `Create` with `DryRun` and zero-valued criteria (unknown
    output at preview) does not error and does not invoke the fake.
14. **Kubeconfig materialization**: contents → file exists during run with
    mode 0600 and is removed after (and on error); path passthrough creates
    no temp file; `context` override rewrites current-context (assert via
    clientcmd reload) and errors on a nonexistent context; ambient → `""`.
15. **ClusterStack criteria output**: existing component test extended —
    resolved stack echoes canonicalized criteria (`" EKS "` → `eks`), unset
    os/platform omitted, nodes echoed.

### Deferred to live testing (GPU rig runbook + kind example)

- Actual agent/validator Job scheduling, incl. tainted GPU node groups via
  `tolerations` (the 001-documented Pending-until-timeout trap).
- Cancellation behavior end-to-end (Ctrl-C mid-run: verify SDK reaps
  RBAC/CRB, adapter sweep reaps the in-flight Job).
- EKS/H100 acceptance: ValidationRun reproducing the CLI campaign's findings
  (os-mismatch readiness failure; `driver.enabled=false` conformance
  failure).
- kind example with `requireGpu: false`: readiness passes, GPU checks
  skip/fail as documented.
