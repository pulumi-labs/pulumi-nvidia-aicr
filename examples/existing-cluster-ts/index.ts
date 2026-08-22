import * as pulumi from "@pulumi/pulumi";
import * as aicr from "@pulumi-labs/nvidia-aicr";

// ============================================================================
// AICR Quickstart — Deploy on an Existing Kubernetes Cluster
//
// The simplest way to deploy NVIDIA AICR. Uses your ambient kubeconfig
// (~/.kube/config) and configurable criteria via `pulumi config`.
//
// Usage:
//   pulumi config set accelerator h100
//   pulumi config set service eks
//   pulumi config set intent training
//   pulumi up
// ============================================================================

const config = new pulumi.Config();

const gpuStack = new aicr.ClusterStack("aicr", {
    // Uses ambient kubeconfig when no kubeconfig/kubeconfigPath is set
    accelerator: config.require("accelerator"),
    service: config.require("service"),
    intent: config.require("intent"),
    platform: config.get("platform"),       // optional
    os: config.get("os"),                   // optional; unset = OS-agnostic recipe
    skipAwait: config.getBoolean("skipAwait") || false,
});

// Validate the deployed stack empirically: a snapshot agent captures cluster
// state, then the recipe's deployment and conformance checks run as Jobs in
// the cluster (~10 minutes). With the default strict: false the update always
// succeeds and the verdict is data — read the exported status and per-check
// results. Set strict: true to fail the update on failed checks instead.
//
// Validation pods tolerate all taints by default, so tainted GPU node groups
// need no configuration; the `tolerations` input exists to narrow that.
const validation = new aicr.ValidationRun("aicr-validation", {
    // Uses the same ambient kubeconfig as the ClusterStack above
    criteria: gpuStack.criteria,              // same recipe as the stack — no drift
    recipeDataVersion: gpuStack.recipeVersion,          // assert same recipe data as deployed
    triggers: [gpuStack.deployedComponents],  // re-validate when the stack changes
});

export const recipeName = gpuStack.recipeName;
export const recipeVersion = gpuStack.recipeVersion;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
export const validationStatus = validation.status;
export const validationChecks = validation.phaseResults;
