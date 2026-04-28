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
// The "self-managed" service type uses the base AICR recipe without
// cloud-specific operators (no aws-efa, aws-ebs-csi-driver, etc.),
// which is appropriate for CoreWeave's bare-metal environment.
//
// CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs)
package main

import (
	aicr "github.com/pulumi/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		kubeconfigPath := cfg.Get("kubeconfigPath")
		if kubeconfigPath == "" {
			kubeconfigPath = "~/.kube/config"
		}

		// Deploy NVIDIA AICR inference stack
		// Components include:
		//   - NVIDIA GPU Operator (driver management, device plugin)
		//   - Dynamo Platform (NVIDIA's inference serving framework)
		//   - KGateway (API gateway for inference endpoints)
		//   - KAI Scheduler (GPU-aware scheduling)
		//   - Monitoring stack (Prometheus, Grafana, DCGM metrics)
		//   - cert-manager, NVSentinel, and more
		inferenceStack, err := aicr.NewClusterStack(ctx, "nvidia-inference", &aicr.ClusterStackArgs{
			KubeconfigPath: pulumi.StringPtr(kubeconfigPath),
			Accelerator:    "h100",
			Service:        "self-managed", // CoreWeave = self-managed K8s
			Intent:         "inference",
			Platform:       pulumi.StringPtr("dynamo"),
			// Skip cloud-specific components not needed on CoreWeave
			SkipComponents: pulumi.StringArray{
				pulumi.String("aws-efa"),
				pulumi.String("aws-ebs-csi-driver"),
			},
			// Customize Dynamo platform for CoreWeave's storage
			ComponentOverrides: aicr.ComponentOverrideMap{
				"dynamo-platform": aicr.ComponentOverrideArgs{
					Values: pulumi.Map{
						"etcd": pulumi.Map{
							"persistence": pulumi.Map{
								"storageClass": pulumi.String("coreweave-ssd"),
							},
						},
						"nats": pulumi.Map{
							"config": pulumi.Map{
								"jetstream": pulumi.Map{
									"fileStore": pulumi.Map{
										"pvc": pulumi.Map{
											"storageClassName": pulumi.String("coreweave-ssd"),
										},
									},
								},
							},
						},
					},
				},
			},
		})
		if err != nil {
			return err
		}

		// Exports
		ctx.Export("recipeName", inferenceStack.RecipeName)
		ctx.Export("deployedComponents", inferenceStack.DeployedComponents)
		ctx.Export("componentCount", inferenceStack.ComponentCount)
		return nil
	})
}
