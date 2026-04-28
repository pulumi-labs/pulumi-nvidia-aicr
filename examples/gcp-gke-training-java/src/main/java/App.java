// GCP GKE + NVIDIA AICR H100 Training Stack
//
// Creates a GKE cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// COST WARNING: a3-highgpu-8g instances cost ~$30/hr each (8x H100 80GB).
// Default nodeCount is 2, so plan on ~$60/hr while the cluster is up.

import java.util.Map;

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
            .componentOverrides(Map.of(
                "gpu-operator", ComponentOverrideArgs.builder()
                    .values(Map.of(
                        "driver", Map.of("version", "580.105.08")))
                    .build()))
            .build(),
            ComponentResourceOptions.builder()
                .dependsOn(gpuNodePool)
                .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());
    }
}
