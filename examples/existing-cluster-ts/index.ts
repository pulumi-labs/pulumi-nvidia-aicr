import * as pulumi from "@pulumi/pulumi";
import * as aicr from "@pulumi/nvidia-aicr";

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
    os: config.get("os"),                   // optional, defaults to ubuntu
    skipAwait: config.getBoolean("skipAwait") || false,
});

export const recipeName = gpuStack.recipeName;
export const recipeVersion = gpuStack.recipeVersion;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
