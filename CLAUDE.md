# tinymon-operator

Kubernetes operator for TinyMon. Watches cluster resources (nodes, deployments, ingresses, PVCs, backups) and pushes their status to TinyMon via the Push API.

## Project Structure

```
main.go                              Entry point, sets up controller-runtime manager + reconcilers
internal/
  controller/
    common.go                        Shared reconciler helpers
    node.go                          Node reconciler (CPU, memory, disk, load, conditions)
    deployment.go                    Deployment reconciler (replica status)
    ingress.go                       Ingress reconciler (HTTP + certificate checks)
    pvc.go                           PVC reconciler (bound/pending status)
    backup.go                        Backup reconciler (K8up backup status)
  tinymon/
    client.go                        TinyMon Push API HTTP client
scripts/
  node-monitor.sh                    DaemonSet script for node-level metrics (disk, S.M.A.R.T.)
charts/tinymon-operator/             Helm chart
Dockerfile                           Operator image
Dockerfile.node-monitor              Node monitor DaemonSet image
```

## Tech Stack

- Go 1.25, controller-runtime (sigs.k8s.io/controller-runtime)
- K8s client-go, metrics API (k8s.io/metrics)
- K8up v2 API for backup monitoring
- Docker images: `unclesamwk/tinymon-operator`, `unclesamwk/tinymon-node-monitor` (amd64 + arm64)

## Key Concepts

- **Reconcilers**: Each resource type has its own reconciler (node, deployment, ingress, PVC, backup)
- **Push API client**: `tinymon.Client` wraps HTTP calls with Bearer auth to TinyMon Push API
- **Host addressing**: Uses `k8s://<cluster>/<resource-type>/<name>` as host address in TinyMon
- **Topic grouping**: Hosts are organized by topic path (e.g. `<cluster>/<namespace>/deployments`)
- **Node monitor**: Optional DaemonSet that collects node-level metrics (disk usage, S.M.A.R.T.) via shell script

## Configuration

Environment variables (set via Helm values):

| Variable | Helm Value | Description |
|----------|------------|-------------|
| `TINYMON_URL` | `tinymon.url` | TinyMon instance base URL |
| `TINYMON_API_KEY` | `tinymon.apiKey` | Push API Bearer token |
| `CLUSTER_NAME` | `tinymon.clusterName` | Cluster identifier for host addressing |

## Build

```bash
go build -o tinymon-operator .
```

## Deploy

```bash
helm repo add tinymon https://unclesamwk.github.io/tinymon-operator
helm install tinymon-operator tinymon/tinymon-operator \
  --set tinymon.url=https://mon.example.com \
  --set tinymon.apiKey=YOUR_KEY \
  --set tinymon.clusterName=production
```

## Versioning

- Current version: v0.2.x
- Tags: increment +0.0.1
- GitHub: unclesamwk/tinymon-operator

## Related Repos

- **TinyMon (MiniMon)**: The monitoring application itself
- **terraform-provider-tinymon**: Terraform provider (for non-K8s infrastructure)
