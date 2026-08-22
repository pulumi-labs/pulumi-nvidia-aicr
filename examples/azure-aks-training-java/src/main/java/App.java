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
import java.util.stream.Collectors;

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
        // Validation pods tolerate all taints by default, so the tainted GPU
        // node pool above needs no configuration; the `tolerations` input exists
        // to narrow that.
        var validation = new ValidationRun("nvidia-aicr-validation", ValidationRunArgs.builder()
            // Keep in sync with the ClusterStack criteria above (Python/TS wire
            // the stack's `criteria` output directly; Java cannot).
            .criteria(RecipeCriteriaArgs.builder()
                .accelerator("h100")
                .service("aks")
                .intent("training")
                .platform("kubeflow")
                .os("ubuntu")
                .build())
            .recipeDataVersion(gpuStack.recipeVersion())        // assert same recipe data as deployed
            .kubeconfig(kubeconfig)
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
