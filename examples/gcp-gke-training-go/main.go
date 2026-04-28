// GCP GKE + NVIDIA AICR H100 Training Stack
//
// This example creates a complete GPU training environment:
//   1. A GKE cluster with a dedicated H100 GPU node pool
//   2. The full AICR-validated software stack for distributed training
//      including GPU Operator, Kubeflow Trainer, monitoring, and more.
//
// COST WARNING: a3-highgpu-8g instances cost approximately $30/hr each
// (8x NVIDIA H100 80GB per node). This example provisions 2 nodes (~$60/hr).
// Remember to run `pulumi destroy` when finished to avoid unexpected charges.
package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/container"
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
		nodeCount := cfg.GetInt("nodeCount")
		if nodeCount == 0 {
			nodeCount = 2
		}

		// Create the GKE cluster (we remove the default node pool and manage our own)
		cluster, err := container.NewCluster(ctx, clusterName, &container.ClusterArgs{
			InitialNodeCount:       pulumi.Int(1),
			RemoveDefaultNodePool:  pulumi.Bool(true),
			DeletionProtection:     pulumi.Bool(false),
			ResourceLabels: pulumi.StringMap{
				"nvidia-aicr": pulumi.String("true"),
				"gpu-type":    pulumi.String("h100"),
			},
		})
		if err != nil {
			return err
		}

		// Create a GPU node pool with A3 High-GPU machines (8x H100 80GB each)
		gpuNodePool, err := container.NewNodePool(ctx, "gpu-pool", &container.NodePoolArgs{
			Cluster:   cluster.Name,
			NodeCount: pulumi.Int(nodeCount),
			NodeConfig: &container.NodePoolNodeConfigArgs{
				MachineType: pulumi.String("a3-highgpu-8g"), // 8x NVIDIA H100 80GB per node
				GuestAccelerators: container.NodePoolNodeConfigGuestAcceleratorArray{
					container.NodePoolNodeConfigGuestAcceleratorArgs{
						Type:  pulumi.String("nvidia-h100-80gb"),
						Count: pulumi.Int(8),
					},
				},
				OauthScopes: pulumi.StringArray{
					pulumi.String("https://www.googleapis.com/auth/cloud-platform"),
				},
				Labels: pulumi.StringMap{
					"nvidia.com/gpu": pulumi.String("h100"),
				},
			},
		})
		if err != nil {
			return err
		}

		// Construct a kubeconfig from the GKE cluster endpoint and CA certificate.
		// Uses gke-gcloud-auth-plugin for exec-based authentication.
		kubeconfig := pulumi.All(cluster.Endpoint, cluster.MasterAuth.ClusterCaCertificate()).ApplyT(
			func(args []interface{}) string {
				endpoint := args[0].(string)
				caCert := args[1].(*string)
				ca := ""
				if caCert != nil {
					ca = *caCert
				}
				return fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: %s
    server: https://%s
  name: gke-cluster
contexts:
- context:
    cluster: gke-cluster
    user: gke-user
  name: gke-context
current-context: gke-context
kind: Config
users:
- name: gke-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gke-gcloud-auth-plugin
      installHint: Install gke-gcloud-auth-plugin for kubeconfig exec auth
      provideClusterInfo: true
`, ca, endpoint)
			},
		).(pulumi.StringOutput)

		// Deploy the NVIDIA AICR-validated GPU training stack
		// This installs ~10 validated Helm charts including:
		//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
		//   - Kubeflow Training Operator (distributed training with TrainJob)
		//   - KAI Scheduler (GPU-aware scheduling)
		//   - Kube Prometheus Stack (monitoring with GPU metrics)
		//   - cert-manager, NVSentinel, Skyhook, and more
		gpuStack, err := aicr.NewClusterStack(ctx, "nvidia-aicr", &aicr.ClusterStackArgs{
			Kubeconfig:  kubeconfig,
			Accelerator: "h100",
			Service:     "gke",
			Intent:      "training",
			Platform:    pulumi.StringPtr("kubeflow"),
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
		}, pulumi.DependsOn([]pulumi.Resource{gpuNodePool}))
		if err != nil {
			return err
		}

		// Exports
		ctx.Export("kubeconfig", pulumi.ToSecret(kubeconfig))
		ctx.Export("recipeName", gpuStack.RecipeName)
		ctx.Export("deployedComponents", gpuStack.DeployedComponents)
		ctx.Export("componentCount", gpuStack.ComponentCount)
		return nil
	})
}
