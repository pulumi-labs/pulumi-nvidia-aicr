// AWS EKS + NVIDIA AICR H100 vLLM Inference Stack
//
// This example creates a complete GPU inference environment:
//   1. An EKS cluster with H100 GPU worker nodes
//   2. The full AICR-validated software stack for vLLM model serving
//      including GPU Operator, NIM Operator, KGateway, monitoring, and more.
//
// The NIM (NVIDIA Inference Microservices) platform provides optimized
// vLLM-based model serving with automatic scaling, health checks, and
// GPU-aware routing via KGateway.
//
// COST WARNING: p5.48xlarge instances cost approximately $98.32/hr each.
// This example provisions 2 nodes (~$196/hr). Remember to run
// `pulumi destroy` when finished to avoid unexpected charges.
package main

import (
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-eks/sdk/v3/go/eks"
	aicr "github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		clusterName := cfg.Get("clusterName")
		if clusterName == "" {
			clusterName = "aicr-inference"
		}

		// Create a VPC for the EKS cluster
		vpc, err := ec2.NewVpc(ctx, "gpu-vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.StringPtr("10.0.0.0/16"),
			EnableDnsHostnames: pulumi.BoolPtr(true),
			EnableDnsSupport:   pulumi.BoolPtr(true),
			Tags:               pulumi.StringMap{"Name": pulumi.Sprintf("%s-vpc", clusterName)},
		})
		if err != nil {
			return err
		}

		publicSubnet1, err := ec2.NewSubnet(ctx, "public-1", &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.StringPtr("10.0.1.0/24"),
			AvailabilityZone:    pulumi.StringPtr("us-east-1a"),
			MapPublicIpOnLaunch: pulumi.BoolPtr(true),
			Tags:                pulumi.StringMap{"Name": pulumi.Sprintf("%s-public-1", clusterName)},
		})
		if err != nil {
			return err
		}

		publicSubnet2, err := ec2.NewSubnet(ctx, "public-2", &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.StringPtr("10.0.2.0/24"),
			AvailabilityZone:    pulumi.StringPtr("us-east-1b"),
			MapPublicIpOnLaunch: pulumi.BoolPtr(true),
			Tags:                pulumi.StringMap{"Name": pulumi.Sprintf("%s-public-2", clusterName)},
		})
		if err != nil {
			return err
		}

		igw, err := ec2.NewInternetGateway(ctx, "igw", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
		})
		if err != nil {
			return err
		}

		routeTable, err := ec2.NewRouteTable(ctx, "public-rt", &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
			Routes: ec2.RouteTableRouteArray{
				ec2.RouteTableRouteArgs{
					CidrBlock: pulumi.StringPtr("0.0.0.0/0"),
					GatewayId: igw.ID(),
				},
			},
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewRouteTableAssociation(ctx, "public-1-rta", &ec2.RouteTableAssociationArgs{
			SubnetId:     publicSubnet1.ID(),
			RouteTableId: routeTable.ID(),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewRouteTableAssociation(ctx, "public-2-rta", &ec2.RouteTableAssociationArgs{
			SubnetId:     publicSubnet2.ID(),
			RouteTableId: routeTable.ID(),
		})
		if err != nil {
			return err
		}

		// Create the EKS cluster with GPU node group
		cluster, err := eks.NewCluster(ctx, clusterName, &eks.ClusterArgs{
			VpcId:                        vpc.ID(),
			SubnetIds:                    pulumi.StringArray{publicSubnet1.ID(), publicSubnet2.ID()},
			InstanceType:                 pulumi.StringPtr("p5.48xlarge"), // 8x NVIDIA H100 80GB per node
			DesiredCapacity:              pulumi.IntPtr(2),
			MinSize:                      pulumi.IntPtr(1),
			MaxSize:                      pulumi.IntPtr(4),
			NodeAssociatePublicIpAddress: pulumi.BoolPtr(false),
			CreateOidcProvider:           pulumi.BoolPtr(true),
			Tags: pulumi.StringMap{
				"nvidia.com/aicr": pulumi.String("true"),
				"nvidia.com/gpu":  pulumi.String("h100"),
			},
		})
		if err != nil {
			return err
		}

		// Deploy the NVIDIA AICR-validated vLLM inference stack
		// This installs the validated Helm charts for NIM-based inference including:
		//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
		//   - NIM Operator (manages NIM custom resources for model serving)
		//   - KGateway (GPU-aware ingress routing for inference endpoints)
		//   - KAI Scheduler (GPU-aware scheduling)
		//   - Kube Prometheus Stack (monitoring with GPU metrics)
		//   - cert-manager, NVSentinel, Skyhook, and more
		gpuStack, err := aicr.NewClusterStack(ctx, "nvidia-aicr", &aicr.ClusterStackArgs{
			Kubeconfig:  cluster.KubeconfigJson,
			Accelerator: "h100",
			Service:     "eks",
			Intent:      "inference",
			Platform:    pulumi.StringPtr("nim"),
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

		// Exports
		ctx.Export("kubeconfig", pulumi.ToSecret(cluster.KubeconfigJson))
		ctx.Export("recipeName", gpuStack.RecipeName)
		ctx.Export("deployedComponents", gpuStack.DeployedComponents)
		ctx.Export("componentCount", gpuStack.ComponentCount)
		return nil
	})
}
