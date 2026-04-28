// Oracle Cloud OKE + NVIDIA AICR GB200 Training Stack
//
// This example creates a complete GPU training environment on OCI:
//   1. A VCN (Virtual Cloud Network) with a subnet
//   2. An OKE (Oracle Kubernetes Engine) cluster
//   3. A GPU node pool with BM.GPU.GB200.4 bare-metal shapes
//   4. The full AICR-validated software stack for distributed training
//      including GPU Operator, Kubeflow Trainer, monitoring, and more.
//
// This is the only example using NVIDIA GB200 (Grace Blackwell) GPUs --
// the newest and most powerful NVIDIA data-center GPU architecture,
// featuring the Blackwell GPU paired with the Grace CPU for maximum
// training throughput.
//
// COST WARNING: BM.GPU.GB200.4 bare-metal instances are premium-priced
// GPU shapes. Contact OCI sales for current pricing as availability and
// rates vary by region. Remember to run `pulumi destroy` when finished
// to avoid unexpected charges.
package main

import (
	aicr "github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/containerengine"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/core"
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
		compartmentId := cfg.Require("compartmentId")
		availabilityDomain := cfg.Require("availabilityDomain")

		// Create a VCN for the OKE cluster
		vcn, err := core.NewVcn(ctx, "gpu-vcn", &core.VcnArgs{
			CompartmentId: pulumi.String(compartmentId),
			CidrBlocks:    pulumi.StringArray{pulumi.String("10.0.0.0/16")},
			DisplayName:   pulumi.Sprintf("%s-vcn", clusterName),
			DnsLabel:      pulumi.StringPtr("gpuvcn"),
		})
		if err != nil {
			return err
		}

		// Create an internet gateway for public access
		internetGateway, err := core.NewInternetGateway(ctx, "igw", &core.InternetGatewayArgs{
			CompartmentId: pulumi.String(compartmentId),
			VcnId:         vcn.ID(),
			DisplayName:   pulumi.Sprintf("%s-igw", clusterName),
		})
		if err != nil {
			return err
		}

		// Create a route table with internet access
		routeTable, err := core.NewRouteTable(ctx, "public-rt", &core.RouteTableArgs{
			CompartmentId: pulumi.String(compartmentId),
			VcnId:         vcn.ID(),
			DisplayName:   pulumi.Sprintf("%s-public-rt", clusterName),
			RouteRules: core.RouteTableRouteRuleArray{
				core.RouteTableRouteRuleArgs{
					NetworkEntityId: internetGateway.ID(),
					Destination:     pulumi.String("0.0.0.0/0"),
					DestinationType: pulumi.StringPtr("CIDR_BLOCK"),
				},
			},
		})
		if err != nil {
			return err
		}

		// Create a security list allowing necessary traffic
		securityList, err := core.NewSecurityList(ctx, "oke-sl", &core.SecurityListArgs{
			CompartmentId: pulumi.String(compartmentId),
			VcnId:         vcn.ID(),
			DisplayName:   pulumi.Sprintf("%s-oke-sl", clusterName),
			EgressSecurityRules: core.SecurityListEgressSecurityRuleArray{
				core.SecurityListEgressSecurityRuleArgs{
					Destination: pulumi.String("0.0.0.0/0"),
					Protocol:    pulumi.String("all"),
				},
			},
			IngressSecurityRules: core.SecurityListIngressSecurityRuleArray{
				core.SecurityListIngressSecurityRuleArgs{
					Source:   pulumi.String("10.0.0.0/16"),
					Protocol: pulumi.String("all"),
				},
				core.SecurityListIngressSecurityRuleArgs{
					Source:   pulumi.String("0.0.0.0/0"),
					Protocol: pulumi.String("6"), // TCP
					TcpOptions: core.SecurityListIngressSecurityRuleTcpOptionsArgs{
						Min: pulumi.IntPtr(6443),
						Max: pulumi.IntPtr(6443),
					},
				},
			},
		})
		if err != nil {
			return err
		}

		// Create a subnet for the OKE cluster and node pool
		subnet, err := core.NewSubnet(ctx, "oke-subnet", &core.SubnetArgs{
			CompartmentId:  pulumi.String(compartmentId),
			VcnId:          vcn.ID(),
			CidrBlock:      pulumi.String("10.0.1.0/24"),
			DisplayName:    pulumi.Sprintf("%s-subnet", clusterName),
			RouteTableId:   routeTable.ID(),
			SecurityListIds: pulumi.StringArray{securityList.ID()},
			DnsLabel:        pulumi.StringPtr("okesubnet"),
		})
		if err != nil {
			return err
		}

		// Create the OKE cluster
		cluster, err := containerengine.NewCluster(ctx, clusterName, &containerengine.ClusterArgs{
			CompartmentId:    pulumi.String(compartmentId),
			VcnId:            vcn.ID(),
			KubernetesVersion: pulumi.String("v1.30.1"),
			Name:             pulumi.String(clusterName),
			Options: containerengine.ClusterOptionsArgs{
				ServiceLbSubnetIds: pulumi.StringArray{subnet.ID()},
				KubernetesNetworkConfig: containerengine.ClusterOptionsKubernetesNetworkConfigArgs{
					PodsCidr:     pulumi.StringPtr("10.244.0.0/16"),
					ServicesCidr: pulumi.StringPtr("10.96.0.0/16"),
				},
			},
		})
		if err != nil {
			return err
		}

		// Create a GPU node pool with NVIDIA GB200 bare-metal shapes.
		// BM.GPU.GB200.4 provides 4 NVIDIA GB200 GPUs per bare-metal node --
		// the most powerful GPU shape available on OCI for AI/ML training.
		_, err = containerengine.NewNodePool(ctx, "gpu-nodes", &containerengine.NodePoolArgs{
			CompartmentId:    pulumi.String(compartmentId),
			ClusterId:        cluster.ID(),
			KubernetesVersion: pulumi.String("v1.30.1"),
			Name:             pulumi.Sprintf("%s-gb200-pool", clusterName),
			NodeShape:        pulumi.String("BM.GPU.GB200.4"),
			NodeConfigDetails: containerengine.NodePoolNodeConfigDetailsArgs{
				Size: pulumi.Int(nodeCount),
				PlacementConfigs: containerengine.NodePoolNodeConfigDetailsPlacementConfigArray{
					containerengine.NodePoolNodeConfigDetailsPlacementConfigArgs{
						AvailabilityDomain: pulumi.String(availabilityDomain),
						SubnetId:           subnet.ID(),
					},
				},
			},
			InitialNodeLabels: containerengine.NodePoolInitialNodeLabelArray{
				containerengine.NodePoolInitialNodeLabelArgs{
					Key:   pulumi.StringPtr("nvidia.com/gpu"),
					Value: pulumi.StringPtr("gb200"),
				},
			},
		})
		if err != nil {
			return err
		}

		// Retrieve kubeconfig from the OKE cluster
		kubeconfig := containerengine.GetClusterKubeConfigOutput(ctx, containerengine.GetClusterKubeConfigOutputArgs{
			ClusterId: cluster.ID(),
		})

		// Deploy the NVIDIA AICR-validated GPU training stack.
		// This installs the full set of validated Helm charts including:
		//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
		//   - Kubeflow Training Operator (distributed training with TrainJob)
		//   - KAI Scheduler (GPU-aware scheduling)
		//   - Kube Prometheus Stack (monitoring with GPU metrics)
		//   - cert-manager, NVSentinel, Skyhook, and more
		//
		// The "gb200" accelerator selects the recipe family optimized for
		// NVIDIA Grace Blackwell architecture.
		gpuStack, err := aicr.NewClusterStack(ctx, "nvidia-aicr", &aicr.ClusterStackArgs{
			Kubeconfig:  kubeconfig.Content(),
			Accelerator: "gb200",
			Service:     "oke",
			Intent:      "training",
			Platform:    pulumi.StringPtr("kubeflow"),
		})
		if err != nil {
			return err
		}

		// Exports
		ctx.Export("recipeName", gpuStack.RecipeName)
		ctx.Export("deployedComponents", gpuStack.DeployedComponents)
		ctx.Export("componentCount", gpuStack.ComponentCount)
		return nil
	})
}
