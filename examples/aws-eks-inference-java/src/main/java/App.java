// AWS EKS + NVIDIA AICR H100 vLLM Inference Stack
//
// Creates an EKS cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated NIM inference stack for vLLM model serving.
//
// The NIM (NVIDIA Inference Microservices) platform provides optimized
// vLLM-based model serving with automatic scaling, health checks, and
// GPU-aware routing via KGateway.
//
// COST WARNING: p5.48xlarge instances cost ~$98.32/hr each. Default
// nodeCount is 2, so plan on ~$196/hr while the cluster is up.

import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.aws.ec2.InternetGateway;
import com.pulumi.aws.ec2.InternetGatewayArgs;
import com.pulumi.aws.ec2.RouteTable;
import com.pulumi.aws.ec2.RouteTableArgs;
import com.pulumi.aws.ec2.RouteTableAssociation;
import com.pulumi.aws.ec2.RouteTableAssociationArgs;
import com.pulumi.aws.ec2.Subnet;
import com.pulumi.aws.ec2.SubnetArgs;
import com.pulumi.aws.ec2.Vpc;
import com.pulumi.aws.ec2.VpcArgs;
import com.pulumi.aws.ec2.inputs.RouteTableRouteArgs;
import com.pulumi.eks.Cluster;
import com.pulumi.eks.ClusterArgs;
import com.pulumi.labs.nvidiaaicr.ClusterStack;
import com.pulumi.labs.nvidiaaicr.ClusterStackArgs;
import com.pulumi.labs.nvidiaaicr.ValidationRun;
import com.pulumi.labs.nvidiaaicr.ValidationRunArgs;
import com.pulumi.labs.nvidiaaicr.inputs.ComponentOverrideArgs;
import com.pulumi.labs.nvidiaaicr.inputs.RecipeCriteriaArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();
        var clusterName = config.get("clusterName").orElse("aicr-inference");
        var nodeCount = config.getInteger("nodeCount").orElse(2);

        var vpc = new Vpc("gpu-vpc", VpcArgs.builder()
            .cidrBlock("10.0.0.0/16")
            .enableDnsHostnames(true)
            .enableDnsSupport(true)
            .tags(Map.of("Name", clusterName + "-vpc"))
            .build());

        var publicSubnet1 = new Subnet("public-1", SubnetArgs.builder()
            .vpcId(vpc.id())
            .cidrBlock("10.0.1.0/24")
            .availabilityZone("us-east-1a")
            .mapPublicIpOnLaunch(true)
            .tags(Map.of("Name", clusterName + "-public-1"))
            .build());

        var publicSubnet2 = new Subnet("public-2", SubnetArgs.builder()
            .vpcId(vpc.id())
            .cidrBlock("10.0.2.0/24")
            .availabilityZone("us-east-1b")
            .mapPublicIpOnLaunch(true)
            .tags(Map.of("Name", clusterName + "-public-2"))
            .build());

        var igw = new InternetGateway("igw", InternetGatewayArgs.builder()
            .vpcId(vpc.id())
            .build());

        var routeTable = new RouteTable("public-rt", RouteTableArgs.builder()
            .vpcId(vpc.id())
            .routes(RouteTableRouteArgs.builder()
                .cidrBlock("0.0.0.0/0")
                .gatewayId(igw.id())
                .build())
            .build());

        new RouteTableAssociation("public-1-rta", RouteTableAssociationArgs.builder()
            .subnetId(publicSubnet1.id())
            .routeTableId(routeTable.id())
            .build());
        new RouteTableAssociation("public-2-rta", RouteTableAssociationArgs.builder()
            .subnetId(publicSubnet2.id())
            .routeTableId(routeTable.id())
            .build());

        var cluster = new Cluster(clusterName, ClusterArgs.builder()
            .vpcId(vpc.id())
            .subnetIds(publicSubnet1.id().applyValue(id1 ->
                publicSubnet2.id().applyValue(id2 -> List.of(id1, id2))).applyValue(o -> o))
            .instanceType("p5.48xlarge")
            .desiredCapacity(nodeCount)
            .minSize(1)
            .maxSize(nodeCount * 2)
            .nodeAssociatePublicIpAddress(false)
            .createOidcProvider(true)
            .tags(Map.of(
                "nvidia.com/aicr", "true",
                "nvidia.com/gpu", "h100"))
            .build());

        var gpuStack = new ClusterStack("nvidia-aicr", ClusterStackArgs.builder()
            .kubeconfig(cluster.kubeconfigJson())
            .accelerator("h100")
            .service("eks")
            .intent("inference")
            .platform("nim")
            .os("ubuntu")
            .componentOverrides(Map.of(
                "gpu-operator", ComponentOverrideArgs.builder()
                    .values(Map.of(
                        "driver", Map.of("version", "580.105.08")))
                    .build()))
            .build());

        // Validate the deployed stack empirically: a snapshot agent captures
        // cluster state, then the recipe's deployment and conformance checks run
        // as Jobs in the cluster (~10 minutes). With the default strict=false the
        // update always succeeds and the verdict is data -- read the exported
        // status and per-check results. Set strict=true to fail the update on
        // failed checks instead.
        //
        // Validation pods tolerate all taints by default, so tainted GPU node
        // groups need no configuration; the `tolerations` input exists to narrow
        // that.
        var validation = new ValidationRun("nvidia-aicr-validation", ValidationRunArgs.builder()
            // Keep in sync with the ClusterStack criteria above (Python/TS wire
            // the stack's `criteria` output directly; Java cannot).
            .criteria(RecipeCriteriaArgs.builder()
                .accelerator("h100")
                .service("eks")
                .intent("inference")
                .platform("nim")
                .os("ubuntu")
                .build())
            .recipeDataVersion(gpuStack.recipeVersion())        // assert same recipe data as deployed
            .kubeconfig(cluster.kubeconfigJson())
            .triggers(gpuStack.deployedComponents())  // re-validate when the stack changes
            .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());
        ctx.export("validationStatus", validation.status());
        // pulumi-java cannot serialize generated output types (CheckResult) as
        // stack outputs -- project to plain maps first.
        ctx.export("validationChecks", validation.phaseResults().applyValue(rs ->
            rs.stream().map(c -> Map.of(
                "name", c.name(),
                "phase", c.phase(),
                "status", c.status(),
                "message", c.message()))
              .collect(Collectors.toList())));
    }
}
