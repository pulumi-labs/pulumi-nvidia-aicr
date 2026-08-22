// Azure AKS + NVIDIA AICR H100 Training Stack
//
// This example creates a complete GPU training environment:
//   1. An AKS cluster with H100 GPU worker nodes
//   2. The full AICR-validated software stack for distributed training
//      including GPU Operator, Kubeflow Trainer, monitoring, and more.
//
// COST WARNING: Standard_ND96isr_H100_v5 VMs cost approximately $40/hr each.
// This example provisions 2 GPU nodes (~$80/hr). Remember to run
// `pulumi destroy` when finished to avoid unexpected charges.
package main

import (
	"encoding/base64"

	"github.com/pulumi/pulumi-azure-native-sdk/containerservice/v2"
	"github.com/pulumi/pulumi-azure-native-sdk/resources/v2"
	aicr "github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		clusterName := cfg.Get("clusterName")
		if clusterName == "" {
			clusterName = "aicr-training"
		}

		// Create a resource group for all resources
		rg, err := resources.NewResourceGroup(ctx, "gpu-rg", &resources.ResourceGroupArgs{
			ResourceGroupName: pulumi.Sprintf("%s-rg", clusterName),
			Tags: pulumi.StringMap{
				"nvidia.com/aicr": pulumi.String("true"),
				"nvidia.com/gpu":  pulumi.String("h100"),
			},
		})
		if err != nil {
			return err
		}

		// Create the AKS cluster with a system node pool and a GPU node pool
		cluster, err := containerservice.NewManagedCluster(ctx, clusterName, &containerservice.ManagedClusterArgs{
			ResourceGroupName: rg.Name,
			ResourceName:      pulumi.String(clusterName),
			DnsPrefix:         pulumi.String(clusterName),
			KubernetesVersion: pulumi.String("1.30"),
			Identity: &containerservice.ManagedClusterIdentityArgs{
				Type: containerservice.ResourceIdentityTypeSystemAssigned,
			},
			AgentPoolProfiles: containerservice.ManagedClusterAgentPoolProfileArray{
				containerservice.ManagedClusterAgentPoolProfileArgs{
					Name:   pulumi.String("system"),
					Mode:   containerservice.AgentPoolModeSystem,
					VmSize: pulumi.String("Standard_D4s_v3"),
					Count:  pulumi.Int(1),
					OsType: containerservice.OSTypeLinux,
				},
				containerservice.ManagedClusterAgentPoolProfileArgs{
					Name:   pulumi.String("gpunodes"),
					Mode:   containerservice.AgentPoolModeUser,
					VmSize: pulumi.String("Standard_ND96isr_H100_v5"), // 8x NVIDIA H100 80GB per node
					Count:  pulumi.Int(2),
					OsType: containerservice.OSTypeLinux,
					NodeLabels: pulumi.StringMap{
						"nvidia.com/gpu.present": pulumi.String("true"),
					},
					NodeTaints: pulumi.StringArray{
						pulumi.String("nvidia.com/gpu=present:NoSchedule"),
					},
				},
			},
			Tags: pulumi.StringMap{
				"nvidia.com/aicr": pulumi.String("true"),
				"nvidia.com/gpu":  pulumi.String("h100"),
			},
		})
		if err != nil {
			return err
		}

		// Retrieve the kubeconfig from the AKS cluster
		kubeconfig := pulumi.All(rg.Name, cluster.Name).ApplyT(
			func(args []interface{}) (string, error) {
				rgName := args[0].(string)
				name := args[1].(string)
				creds, err := containerservice.ListManagedClusterUserCredentials(ctx, &containerservice.ListManagedClusterUserCredentialsArgs{
					ResourceGroupName: rgName,
					ResourceName:      name,
				})
				if err != nil {
					return "", err
				}
				decoded, err := base64.StdEncoding.DecodeString(creds.Kubeconfigs[0].Value)
				if err != nil {
					return "", err
				}
				return string(decoded), nil
			},
		).(pulumi.StringOutput)

		// Deploy the NVIDIA AICR-validated GPU training stack
		// This installs ~11 validated Helm charts including:
		//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
		//   - Kubeflow Training Operator (distributed training with TrainJob)
		//   - KAI Scheduler (GPU-aware scheduling)
		//   - Kube Prometheus Stack (monitoring with GPU metrics)
		//   - cert-manager, NVSentinel, Skyhook, and more
		gpuStack, err := aicr.NewClusterStack(ctx, "nvidia-aicr", &aicr.ClusterStackArgs{
			Kubeconfig:  kubeconfig,
			Accelerator: "h100",
			Service:     "aks",
			Intent:      "training",
			Platform:    pulumi.StringRef("kubeflow"),
			Os:          pulumi.StringRef("ubuntu"),
			// Optional: customize specific components
			ComponentOverrides: aicr.ComponentOverrideMap{
				"gpu-operator": aicr.ComponentOverrideArgs{
					Values: pulumi.Map{
						"driver": pulumi.Map{
							// Use a specific driver version if needed
							"version": pulumi.String("580.105.08"),
						},
					},
				},
			},
		})
		if err != nil {
			return err
		}

		// Validate the deployed stack empirically: a snapshot agent captures cluster
		// state, then the recipe's deployment and conformance checks run as Jobs in
		// the cluster (~10 minutes; the GPU node pool's nvidia.com/gpu taint needs
		// no configuration -- validation pods tolerate all taints by default, and
		// the `Tolerations` input exists to narrow that). With the default
		// strict=false the update always succeeds and the verdict is data -- read
		// the exported status and per-check results. Set Strict: pulumi.Bool(true)
		// to fail the update on failed checks instead.
		validation, err := aicr.NewValidationRun(ctx, "nvidia-aicr-validation", &aicr.ValidationRunArgs{
			// Keep in sync with the ClusterStack criteria above (Python/TS wire the stack's `criteria` output directly; Go cannot).
			Criteria: aicr.RecipeCriteriaArgs{
				Accelerator: pulumi.String("h100"),
				Service:     pulumi.String("aks"),
				Intent:      pulumi.String("training"),
				Platform:    pulumi.StringPtr("kubeflow"),
				Os:          pulumi.StringPtr("ubuntu"),
			},
			RecipeDataVersion: gpuStack.RecipeVersion, // assert same recipe data as deployed
			Kubeconfig: kubeconfig,
			Triggers:   pulumi.Array{gpuStack.DeployedComponents}, // re-validate when the stack changes
		})
		if err != nil {
			return err
		}

		// Exports
		ctx.Export("kubeconfig", pulumi.ToSecret(kubeconfig))
		ctx.Export("recipeName", gpuStack.RecipeName)
		ctx.Export("deployedComponents", gpuStack.DeployedComponents)
		ctx.Export("componentCount", gpuStack.ComponentCount)
		ctx.Export("validationStatus", validation.Status)
		ctx.Export("validationChecks", validation.PhaseResults)
		return nil
	})
}
