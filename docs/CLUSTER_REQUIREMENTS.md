# CBSE cluster requirements

This document states the runtime cluster requirements for installing and running CBSE. It is the single source that the future Helm chart's prerequisites and the user-facing install guide should reference. Build-time Go module versions and the smoke harness's locked source images are separate concerns; this page is about the cluster that runs CBSE.

## Kubernetes version

CBSE supports conformant Kubernetes `1.x` API servers at version **1.30 or any later minor release**. 1.30 is the minimum; there is no feature-defined upper minor bound. Distribution suffixes in `gitVersion` (for example K3s build metadata) do not change the major/minor decision. The smoke harness reads the server version during preflight, requires major `1` and minor `>= 30`, imposes no maximum-minor check, and fails before any cluster or registry mutation when the minimum is not met.

## Required feature gate: UserNamespacesSupport

The reference Translator runs a **rootless BuildKit sidecar** (`moby/buildkit:v0.32.2-rootless`) that builds and pushes each scenario's generated runner image. Rootless `buildkitd` must run as the **mapped root inside a user namespace**; it cannot run as root in the host namespace and it cannot run unprivileged without a user namespace. The alpha4 Operator requests this by setting `hostUsers: false` on the Translator Pod and running the `buildkit` container as `runAsUser: 0` (mapped root), with `--oci-worker-no-process-sandbox`, an unconfined seccomp profile, and an unconfined AppArmor profile.

This requires the cluster to have the **`UserNamespacesSupport` feature gate enabled**. It is beta in Kubernetes 1.30–1.32 (disabled by default) and GA in 1.33. On a cluster where the gate is off, the API server strips `hostUsers: false`, no user namespace is provisioned, and rootless `buildkitd` fails at startup:

```
buildkitd: can't enable NoProcessSandbox without Rootless
```

`UserNamespacesSupport` MUST be enabled on the kube-apiserver, kube-controller-manager, and kubelet. Enabling it is a one-time cluster-admin operation outside the CBSE install; the CBSE harness and Operator never enable or assume it.

### K3s enablement

On a K3s server node, edit (or create) `/etc/rancher/k3s/config.yaml`:

```yaml
kube-apiserver-arg:
  - "feature-gates=UserNamespacesSupport=true"
kube-controller-manager-arg:
  - "feature-gates=UserNamespacesSupport=true"
kubelet-arg:
  - "feature-gates=UserNamespacesSupport=true"
```

Restart K3s and verify:

```bash
sudo systemctl restart k3s          # server node (also restarts the local kubelet)
# On additional agent nodes, set the same kubelet-arg in /etc/rancher/k3s/agent/kubelet-arg
# or the agent config, then: sudo systemctl restart k3s-agent

# Verify the gate is on (expect "= 1"):
kubectl get --raw /metrics | grep 'kubernetes_feature_enabled{name="UserNamespacesSupport"'
```

### Prerequisites the feature gate depends on

- **Kernel user namespaces**: `/proc/sys/user/max_user_namespaces` must be greater than `0`, and unprivileged user namespaces must not be disabled (`/proc/sys/kernel/unprivileged_userns_clone` absent or `1`). These are the default on modern Linux.
- **containerd**: K3s bundles a containerd that supports user namespaces (containerd 1.7 or newer). No extra containerd configuration is required for K3s.
- **kubelet CPU manager policy**: must be `none` (the K3s default). The `static` CPU manager policy is incompatible with user namespaces.

### Verifying a user namespace is actually provisioned

After enabling the gate, confirm a Pod with `hostUsers: false` gets a real user-namespace mapping (not the host identity map `0 0 4294967295`):

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata: { name: userns-check }
spec:
  hostUsers: false
  containers:
    - name: check
      image: busybox:1.36
      command: ["sh", "-c", "cat /proc/self/uid_map; sleep 3600"]
  restartPolicy: Never
EOF
sleep 3
kubectl exec userns-check -- cat /proc/self/uid_map   # expect a mapped line, not "0          0 4294967295"
kubectl delete pod userns-check --ignore-not-found
```

## Experiment-namespace Pod Security

The rootless BuildKit sidecar uses an **unconfined seccomp profile** and an **unconfined AppArmor profile** (the spec-documented rootless BuildKit compatibility exception). It does **not** use privileged mode, host networking, host paths, host runtime sockets, or privilege escalation. On a cluster that enforces Pod Security Standards, the experiment namespace must permit these unconfined profiles: the enforce level must be `privileged` (or an equivalent policy exception). The smoke harness labels its ephemeral namespace `pod-security.kubernetes.io/enforce=privileged` (with `audit=restricted` and `warn=restricted`). A production install must apply the same enforce level to every namespace that will host `SimulationExperiment` resources.

## Native sidecar feature gate (1.30–1.32 only)

CBSE's runner Job template allows the Kubernetes `native-sidecar` containers feature. On Kubernetes 1.30 through 1.32 this requires the `SidecarContainers` feature gate to be enabled (it is beta, default-on, in 1.29–1.31 and GA in 1.32+; confirm it is on if your distribution disables beta feature gates by default). On 1.33 and later it is always on.

## Smoke-only node and registry requirements

The mandatory smoke suite additionally requires:

- At least one `Ready` node whose `kubernetes.io/arch` label is exactly `amd64` (the smoke image set is `linux/amd64` only). The node check does not inspect taints, allocatable capacity, or resource pressure.
- A reachable `registry.unibw.de` Docker configuration (`CBSE_REGISTRY_AUTH_FILE`) authorizing pull/push and artifact list/read/delete in the Harbor project `i31bdase`. Outside the smoke profile the deployment owner supplies the `cbse-registry-auth` `kubernetes.io/dockerconfigjson` Secret independently; CBSE fixes its name and validation, not a production account.

## Summary for a future Helm chart

- Kubernetes >= 1.30.
- `UserNamespacesSupport` feature gate enabled (kube-apiserver, kube-controller-manager, kubelet).
- Experiment namespaces labeled `pod-security.kubernetes.io/enforce=privileged` (audit/warn may stay `restricted`).
- A `cbse-registry-auth` Docker-config Secret in each experiment namespace.
- The checked-in CRD serves and stores only `experiment.cbse.terministic.de/alpha4`; alpha2/alpha3 must not be served (the one-time breaking upgrade is a cluster-admin operation the chart must document, not perform automatically).
