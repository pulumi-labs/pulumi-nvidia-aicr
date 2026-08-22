// GCP GKE + NVIDIA AICR H100 Training Stack
//
// Creates a GKE cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// COST WARNING: a3-highgpu-8g instances cost ~$30/hr each (8x H100 80GB).
// Default nodeCount is 2, so plan on ~$60/hr while the cluster is up.

import java.util.Map;
import java.util.stream.Collectors;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.core.Output;
import com.pulumi.resources.ComponentResourceOptions;
import com.pulumi.gcp.container.Cluster;
import com.pulumi.gcp.container.ClusterArgs;
import com.pulumi.gcp.container.NodePool;
import com.pulumi.gcp.container.NodePoolArgs;
import com.pulumi.gcp.container.inputs.NodePoolNodeConfigArgs;
import com.pulumi.gcp.container.inputs.NodePoolNodeConfigGuestAcceleratorArgs;
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

        // Create the GKE cluster (we remove the default node pool and manage our own)
        var cluster = new Cluster(clusterName, ClusterArgs.builder()
            .initialNodeCount(1)
            .removeDefaultNodePool(true)
            .deletionProtection(false)
            .resourceLabels(Map.of(
                "nvidia-aicr", "true",
                "gpu-type", "h100"))
            .build());

        // Create a GPU node pool with A3 High-GPU machines (8x H100 80GB each)
        var gpuNodePool = new NodePool("gpu-pool", NodePoolArgs.builder()
            .cluster(cluster.name())
            .nodeCount(nodeCount)
            .nodeConfig(NodePoolNodeConfigArgs.builder()
                .machineType("a3-highgpu-8g")   // 8x NVIDIA H100 80GB per node
                .guestAccelerators(NodePoolNodeConfigGuestAcceleratorArgs.builder()
                    .type("nvidia-h100-80gb")
                    .count(8)
                    .build())
                .oauthScopes("https://www.googleapis.com/auth/cloud-platform")
                .labels(Map.of("nvidia.com/gpu", "h100"))
                .build())
            .build());

        // Construct a kubeconfig from the GKE cluster endpoint and CA certificate.
        // Uses gke-gcloud-auth-plugin for exec-based authentication.
        var kubeconfig = Output.all(
            cluster.endpoint(),
            cluster.masterAuth().applyValue(ma -> ma.clusterCaCertificate().orElse(""))
        ).applyValue(args2 -> {
            var endpoint = args2.t1();
            var caCert = args2.t2();
            return String.format(
                "apiVersion: v1\n" +
                "clusters:\n" +
                "- cluster:\n" +
                "    certificate-authority-data: %s\n" +
                "    server: https://%s\n" +
                "  name: gke-cluster\n" +
                "contexts:\n" +
                "- context:\n" +
                "    cluster: gke-cluster\n" +
                "    user: gke-user\n" +
                "  name: gke-context\n" +
                "current-context: gke-context\n" +
                "kind: Config\n" +
                "users:\n" +
                "- name: gke-user\n" +
                "  user:\n" +
                "    exec:\n" +
                "      apiVersion: client.authentication.k8s.io/v1beta1\n" +
                "      command: gke-gcloud-auth-plugin\n" +
                "      installHint: Install gke-gcloud-auth-plugin for kubeconfig exec auth\n" +
                "      provideClusterInfo: true\n",
                caCert, endpoint);
        });

        // Deploy the NVIDIA AICR-validated GPU training stack
        // This installs ~10 validated Helm charts including:
        //   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
        //   - Kubeflow Training Operator (distributed training with TrainJob)
        //   - KAI Scheduler (GPU-aware scheduling)
        //   - Kube Prometheus Stack (monitoring with GPU metrics)
        //   - cert-manager, NVSentinel, Skyhook, and more
        var gpuStack = new ClusterStack("nvidia-aicr", ClusterStackArgs.builder()
            .kubeconfig(kubeconfig)
            .accelerator("h100")
            .service("gke")
            .intent("training")
            .platform("kubeflow")
            .os("cos")
            .componentOverrides(Map.of(
                "gpu-operator", ComponentOverrideArgs.builder()
                    .values(Map.of(
                        "driver", Map.of("version", "580.105.08")))
                    .build()))
            .build(),
            ComponentResourceOptions.builder()
                .dependsOn(gpuNodePool)
                .build());

        // Validate the deployed stack empirically: a snapshot agent captures
        // cluster state, then the recipe's deployment and conformance checks run
        // as Jobs in the cluster (~10 minutes; GKE's automatic nvidia.com/gpu
        // taint needs no configuration -- validation pods tolerate all taints by
        // default). With the default strict=false the update always succeeds and
        // the verdict is data; set strict=true to gate downstream resources on a
        // passing run instead.
        var validation = new ValidationRun("nvidia-aicr-validation", ValidationRunArgs.builder()
            // Keep in sync with the ClusterStack criteria above (Python/TS wire
            // the stack's `criteria` output directly; Java cannot).
            .criteria(RecipeCriteriaArgs.builder()
                .accelerator("h100")
                .service("gke")
                .intent("training")
                .platform("kubeflow")
                .os("cos")
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
