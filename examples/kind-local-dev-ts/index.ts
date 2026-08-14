import * as pulumi from "@pulumi/pulumi";
import * as aicr from "@pulumi-labs/nvidia-aicr";

// ============================================================================
// AICR on a local kind cluster — for development of the deployment pipeline
// without real GPU hardware.
//
// Prerequisites:
//   - kind installed and a cluster running:
//       kind create cluster --name aicr-dev
//   - kubectl context pointing at it (kind sets this automatically)
//
// What it does:
//   The `kind` overlay disables driver installation (assumes the host has
//   pre-installed NVIDIA drivers via nvkind, if any) and several other
//   GPU-Operator subcomponents that would otherwise hang in a kind cluster.
//   Many GPU pods will not actually be Ready, but the Helm releases will
//   install — which is enough for iterating on the deployment graph.
//
// Skip these components by default since they require state the kind
// cluster won't have:
//   - aws-efa, aws-ebs-csi-driver: cloud-only
//   - kube-prometheus-stack: heavy; turn back on if you need metrics
// ============================================================================

const config = new pulumi.Config();
const intent = config.get("intent") || "inference";

const stack = new aicr.ClusterStack("kind-aicr", {
    accelerator: "h100",
    service: "kind",
    intent: intent,
    skipAwait: true, // kind clusters often can't satisfy GPU readiness; don't block
    skipComponents: [
        "kube-prometheus-stack",
    ],
});

export const recipeName = stack.recipeName;
export const recipeVersion = stack.recipeVersion;
export const deployedComponents = stack.deployedComponents;
export const componentCount = stack.componentCount;

// Optional: run the recipe's empirical validation against the cluster
// (config: `pulumi config set validate true`). Adds ~10 minutes to the update.
//
// Honest expectations on a GPU-less kind cluster: the readiness pre-flight
// passes (kind recipes bind no OS or GPU constraints), but deployment health
// checks for GPU components fail or skip, and GPU conformance checks skip —
// there is no GPU to validate. Useful for exercising the validation pipeline
// itself; a real verdict needs real hardware (see the EKS/GKE examples).
let validation: aicr.ValidationRun | undefined;
if (config.getBoolean("validate")) {
    validation = new aicr.ValidationRun("kind-validation", {
        criteria: stack.criteria,              // same recipe as the stack
        recipeDataVersion: stack.recipeVersion,          // assert same recipe data as deployed
        requireGpu: false,                     // kind has no GPU nodes
        triggers: [stack.deployedComponents],  // re-run when the stack changes
    });
}

export const validationStatus = validation?.status;
export const validationChecks = validation?.phaseResults;
