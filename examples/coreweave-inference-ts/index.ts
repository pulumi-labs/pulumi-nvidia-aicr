import * as pulumi from "@pulumi/pulumi";
import * as aicr from "@pulumi-labs/nvidia-aicr";

// ============================================================================
// CoreWeave + NVIDIA AICR H100 Inference Stack
//
// This example deploys the AICR-validated inference stack on a CoreWeave
// Kubernetes cluster. CoreWeave provides bare-metal GPU access at
// significantly lower cost than hyperscaler equivalents.
//
// Prerequisites:
//   - A CoreWeave Kubernetes cluster with H100 GPU nodes
//   - kubeconfig configured for the cluster
//
// AICR doesn't ship a dedicated CoreWeave overlay yet, so this example uses
// the EKS H100 inference recipe as the closest match (standard GPU operator
// config that installs drivers, with cloud-specific add-ons skipped via
// `skipComponents`).
//
// CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs).
// ============================================================================

const config = new pulumi.Config();
const kubeconfigPath = config.get("kubeconfigPath") || "~/.kube/config";

// Deploy NVIDIA AICR inference stack
// Components include:
//   - NVIDIA GPU Operator (driver management, device plugin)
//   - Dynamo Platform (NVIDIA's inference serving framework)
//   - KGateway (API gateway for inference endpoints)
//   - KAI Scheduler (GPU-aware scheduling)
//   - Monitoring stack (Prometheus, Grafana, DCGM metrics)
//   - cert-manager, NVSentinel, and more
const inferenceStack = new aicr.ClusterStack("nvidia-inference", {
    kubeconfigPath: kubeconfigPath,
    accelerator: "h100",
    service: "eks",  // closest match; cloud-specific add-ons skipped below
    intent: "inference",
    platform: "dynamo",
    os: "ubuntu",
    // Skip cloud-specific components not needed on CoreWeave
    skipComponents: [
        "aws-efa",
        "aws-ebs-csi-driver",
    ],
    // Customize Dynamo platform for CoreWeave's storage
    componentOverrides: {
        "dynamo-platform": {
            values: {
                etcd: {
                    persistence: {
                        storageClass: "coreweave-ssd",
                    },
                },
                nats: {
                    config: {
                        jetstream: {
                            fileStore: {
                                pvc: {
                                    storageClassName: "coreweave-ssd",
                                },
                            },
                        },
                    },
                },
            },
        },
    },
});

// Validate the deployed stack empirically: a snapshot agent captures cluster
// state, then the recipe's deployment and conformance checks run as Jobs in
// the cluster (~10 minutes). With the default strict: false the update always
// succeeds and the verdict is data — read the exported status and per-check
// results. Set strict: true to fail the update on failed checks instead.
//
// Validation pods tolerate all taints by default, so tainted GPU node groups
// need no configuration; the `tolerations` input exists to narrow that.
const validation = new aicr.ValidationRun("nvidia-inference-validation", {
    criteria: inferenceStack.criteria,             // same recipe as the stack — no drift
    recipeDataVersion: inferenceStack.recipeVersion,         // assert same recipe data as deployed
    kubeconfigPath: kubeconfigPath,                // same kubeconfig the stack uses
    triggers: [inferenceStack.deployedComponents], // re-validate when the stack changes
});

// Exports
export const recipeName = inferenceStack.recipeName;
export const deployedComponents = inferenceStack.deployedComponents;
export const componentCount = inferenceStack.componentCount;
export const validationStatus = validation.status;
export const validationChecks = validation.phaseResults;
