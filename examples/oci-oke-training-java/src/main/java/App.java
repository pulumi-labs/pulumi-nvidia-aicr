// Oracle Cloud OKE + NVIDIA AICR GB200 Training Stack
//
// Creates an OKE cluster with GB200 bare-metal GPU nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// This is the only example using NVIDIA GB200 (Grace Blackwell) GPUs --
// the newest and most powerful NVIDIA data-center GPU architecture.
//
// COST WARNING: BM.GPU.GB200.4 bare-metal instances are premium-priced.
// Contact OCI sales for current pricing. Destroy when done!

import java.util.List;
import java.util.Map;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.oci.Core.InternetGateway;
import com.pulumi.oci.Core.InternetGatewayArgs;
import com.pulumi.oci.Core.RouteTable;
import com.pulumi.oci.Core.RouteTableArgs;
import com.pulumi.oci.Core.SecurityList;
import com.pulumi.oci.Core.SecurityListArgs;
import com.pulumi.oci.Core.Subnet;
import com.pulumi.oci.Core.SubnetArgs;
import com.pulumi.oci.Core.Vcn;
import com.pulumi.oci.Core.VcnArgs;
import com.pulumi.oci.Core.inputs.RouteTableRouteRuleArgs;
import com.pulumi.oci.Core.inputs.SecurityListEgressSecurityRuleArgs;
import com.pulumi.oci.Core.inputs.SecurityListIngressSecurityRuleArgs;
import com.pulumi.oci.Core.inputs.SecurityListIngressSecurityRuleTcpOptionsArgs;
import com.pulumi.oci.ContainerEngine.Cluster;
import com.pulumi.oci.ContainerEngine.ClusterArgs;
import com.pulumi.oci.ContainerEngine.NodePool;
import com.pulumi.oci.ContainerEngine.NodePoolArgs;
import com.pulumi.oci.ContainerEngine.ContainerEngineFunctions;
import com.pulumi.oci.ContainerEngine.inputs.GetClusterKubeConfigArgs;
import com.pulumi.oci.ContainerEngine.inputs.ClusterOptionsArgs;
import com.pulumi.oci.ContainerEngine.inputs.ClusterOptionsKubernetesNetworkConfigArgs;
import com.pulumi.oci.ContainerEngine.inputs.NodePoolNodeConfigDetailsArgs;
import com.pulumi.oci.ContainerEngine.inputs.NodePoolNodeConfigDetailsPlacementConfigArgs;
import com.pulumi.oci.ContainerEngine.inputs.NodePoolInitialNodeLabelArgs;
import com.pulumi.nvidiaaicr.ClusterStack;
import com.pulumi.nvidiaaicr.ClusterStackArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();
        var clusterName = config.get("clusterName").orElse("aicr-training");
        var nodeCount = config.getInteger("nodeCount").orElse(2);
        var compartmentId = config.require("compartmentId");
        var availabilityDomain = config.require("availabilityDomain");

        // Create a VCN for the OKE cluster
        var vcn = new Vcn("gpu-vcn", VcnArgs.builder()
            .compartmentId(compartmentId)
            .cidrBlocks("10.0.0.0/16")
            .displayName(clusterName + "-vcn")
            .dnsLabel("gpuvcn")
            .build());

        // Create an internet gateway for public access
        var internetGateway = new InternetGateway("igw", InternetGatewayArgs.builder()
            .compartmentId(compartmentId)
            .vcnId(vcn.id())
            .displayName(clusterName + "-igw")
            .build());

        // Create a route table with internet access
        var routeTable = new RouteTable("public-rt", RouteTableArgs.builder()
            .compartmentId(compartmentId)
            .vcnId(vcn.id())
            .displayName(clusterName + "-public-rt")
            .routeRules(RouteTableRouteRuleArgs.builder()
                .networkEntityId(internetGateway.id())
                .destination("0.0.0.0/0")
                .destinationType("CIDR_BLOCK")
                .build())
            .build());

        // Create a security list allowing necessary traffic
        var securityList = new SecurityList("oke-sl", SecurityListArgs.builder()
            .compartmentId(compartmentId)
            .vcnId(vcn.id())
            .displayName(clusterName + "-oke-sl")
            .egressSecurityRules(SecurityListEgressSecurityRuleArgs.builder()
                .destination("0.0.0.0/0")
                .protocol("all")
                .build())
            .ingressSecurityRules(
                SecurityListIngressSecurityRuleArgs.builder()
                    .source("10.0.0.0/16")
                    .protocol("all")
                    .build(),
                SecurityListIngressSecurityRuleArgs.builder()
                    .source("0.0.0.0/0")
                    .protocol("6") // TCP
                    .tcpOptions(SecurityListIngressSecurityRuleTcpOptionsArgs.builder()
                        .min(6443)
                        .max(6443)
                        .build())
                    .build())
            .build());

        // Create a subnet for the OKE cluster and node pool
        var subnet = new Subnet("oke-subnet", SubnetArgs.builder()
            .compartmentId(compartmentId)
            .vcnId(vcn.id())
            .cidrBlock("10.0.1.0/24")
            .displayName(clusterName + "-subnet")
            .routeTableId(routeTable.id())
            .securityListIds(securityList.id().applyValue(List::of))
            .dnsLabel("okesubnet")
            .build());

        // Create the OKE cluster
        var cluster = new Cluster(clusterName, ClusterArgs.builder()
            .compartmentId(compartmentId)
            .vcnId(vcn.id())
            .kubernetesVersion("v1.30.1")
            .name(clusterName)
            .options(ClusterOptionsArgs.builder()
                .serviceLbSubnetIds(subnet.id().applyValue(List::of))
                .kubernetesNetworkConfig(ClusterOptionsKubernetesNetworkConfigArgs.builder()
                    .podsCidr("10.244.0.0/16")
                    .servicesCidr("10.96.0.0/16")
                    .build())
                .build())
            .build());

        // Create a GPU node pool with NVIDIA GB200 bare-metal shapes.
        // BM.GPU.GB200.4 provides 4 NVIDIA GB200 GPUs per bare-metal node --
        // the most powerful GPU shape available on OCI for AI/ML training.
        new NodePool("gpu-nodes", NodePoolArgs.builder()
            .compartmentId(compartmentId)
            .clusterId(cluster.id())
            .kubernetesVersion("v1.30.1")
            .name(clusterName + "-gb200-pool")
            .nodeShape("BM.GPU.GB200.4")
            .nodeConfigDetails(NodePoolNodeConfigDetailsArgs.builder()
                .size(nodeCount)
                .placementConfigs(NodePoolNodeConfigDetailsPlacementConfigArgs.builder()
                    .availabilityDomain(availabilityDomain)
                    .subnetId(subnet.id())
                    .build())
                .build())
            .initialNodeLabels(NodePoolInitialNodeLabelArgs.builder()
                .key("nvidia.com/gpu")
                .value("gb200")
                .build())
            .build());

        // Retrieve kubeconfig from the OKE cluster
        var kubeconfig = ContainerEngineFunctions.getClusterKubeConfig(GetClusterKubeConfigArgs.builder()
            .clusterId(cluster.id())
            .build());

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
        var gpuStack = new ClusterStack("nvidia-aicr", ClusterStackArgs.builder()
            .kubeconfig(kubeconfig.applyValue(kc -> kc.content()))
            .accelerator("gb200")
            .service("oke")
            .intent("training")
            .platform("kubeflow")
            .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());
    }
}
