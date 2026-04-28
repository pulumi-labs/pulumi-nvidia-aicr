// Azure AKS + NVIDIA AICR H100 Training Stack
//
// Creates an AKS cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// COST WARNING: Standard_ND96isr_H100_v5 VMs cost ~$40/hr each. Default
// nodeCount is 2, so plan on ~$80/hr while the cluster is up.

import java.util.Base64;
import java.util.List;
import java.util.Map;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.azurenative.containerservice.ContainerServiceFunctions;
import com.pulumi.azurenative.containerservice.ManagedCluster;
import com.pulumi.azurenative.containerservice.ManagedClusterArgs;
import com.pulumi.azurenative.containerservice.enums.AgentPoolMode;
import com.pulumi.azurenative.containerservice.enums.OSType;
import com.pulumi.azurenative.containerservice.enums.ResourceIdentityType;
import com.pulumi.azurenative.containerservice.inputs.ListManagedClusterUserCredentialsArgs;
import com.pulumi.azurenative.containerservice.inputs.ManagedClusterAgentPoolProfileArgs;
import com.pulumi.azurenative.containerservice.inputs.ManagedClusterIdentityArgs;
import com.pulumi.azurenative.resources.ResourceGroup;
import com.pulumi.azurenative.resources.ResourceGroupArgs;
import com.pulumi.core.Output;
import com.pulumi.nvidiaaicr.ClusterStack;
import com.pulumi.nvidiaaicr.ClusterStackArgs;
import com.pulumi.nvidiaaicr.inputs.ComponentOverrideArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();
        var clusterName = config.get("clusterName").orElse("aicr-training");
        var nodeCount = config.getInteger("nodeCount").orElse(2);

        var resourceGroup = new ResourceGroup("gpu-rg", ResourceGroupArgs.builder()
            .resourceGroupName(clusterName + "-rg")
            .tags(Map.of(
                "nvidia.com/aicr", "true",
                "nvidia.com/gpu", "h100"))
            .build());

        var cluster = new ManagedCluster(clusterName, ManagedClusterArgs.builder()
            .resourceGroupName(resourceGroup.name())
            .resourceName(clusterName)
            .dnsPrefix(clusterName)
            .kubernetesVersion("1.30")
            .identity(ManagedClusterIdentityArgs.builder()
                .type(ResourceIdentityType.SystemAssigned)
                .build())
            .agentPoolProfiles(
                ManagedClusterAgentPoolProfileArgs.builder()
                    .name("system")
                    .mode(AgentPoolMode.System)
                    .vmSize("Standard_D4s_v3")
                    .count(1)
                    .osType(OSType.Linux)
                    .build(),
                ManagedClusterAgentPoolProfileArgs.builder()
                    .name("gpunodes")
                    .mode(AgentPoolMode.User)
                    .vmSize("Standard_ND96isr_H100_v5") // 8x NVIDIA H100 80GB per node
                    .count(nodeCount)
                    .osType(OSType.Linux)
                    .nodeLabels(Map.of("nvidia.com/gpu.present", "true"))
                    .nodeTaints("nvidia.com/gpu=present:NoSchedule")
                    .build())
            .tags(Map.of(
                "nvidia.com/aicr", "true",
                "nvidia.com/gpu", "h100"))
            .build());

        // Retrieve the kubeconfig from the AKS cluster
        var kubeconfig = Output.tuple(resourceGroup.name(), cluster.name()).applyValue(t -> {
            return ContainerServiceFunctions.listManagedClusterUserCredentials(
                ListManagedClusterUserCredentialsArgs.builder()
                    .resourceGroupName(t.t1)
                    .resourceName(t.t2)
                    .build());
        }).applyValue(creds -> {
            var encoded = creds.kubeconfigs().get(0).value();
            return new String(Base64.getDecoder().decode(encoded));
        });

        var gpuStack = new ClusterStack("nvidia-aicr", ClusterStackArgs.builder()
            .kubeconfig(kubeconfig)
            .accelerator("h100")
            .service("aks")
            .intent("training")
            .platform("kubeflow")
            .componentOverrides(Map.of(
                "gpu-operator", ComponentOverrideArgs.builder()
                    .values(Map.of(
                        "driver", Map.of("version", "580.105.08")))
                    .build()))
            .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());
    }
}
