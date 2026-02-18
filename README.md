# TinyMon Operator

Kubernetes operator that watches cluster resources and syncs them to [TinyMon](https://github.com/unclesamwk/TinyMon) via the Push API.

Annotate your Kubernetes resources with `tinymon.io/enabled: "true"` and the operator will automatically create hosts and checks in TinyMon.

## Watched Resources

| Resource | Check Types | Mode | Status Mapping |
|----------|------------|------|-----------------|
| **Node** | load, memory, disk | Push | CPU/Memory via Metrics API: ok <80%, warning 80-90%, critical >90%. Disk via Kubelet Stats. |
| **Deployment** | status | Push | All replicas ready = ok, partial = warning, none = critical |
| **Ingress** | http, certificate, icecast_listeners | Pull | Created in TinyMon, executed by TinyMon (not pushed by operator) |
| **PVC** | disk | Push | Bound = ok, Pending = warning, Lost = critical. Value: requested size in GB. |
| **K8up Schedule** | status | Push | Lists Backup objects: Completed = ok, Failed = critical, >48h stale = warning |

**Push**: The operator pushes check results to TinyMon via the Bulk API.
**Pull**: The operator only creates the checks in TinyMon. TinyMon executes them independently.

## Node Monitor DaemonSet

The operator includes an optional **Node Monitor DaemonSet** that runs on every node and collects real OS-level metrics, replacing the limited data from the Kubernetes Metrics API:

| Metric | Source | What it measures |
|--------|--------|------------------|
| **Disk** | `df` via `/proc/1/mounts` | All real filesystems (root, boot, ZFS, NFS, ...) with per-mount usage |
| **Memory** | `/proc/meminfo` | OS-level MemTotal / MemAvailable |
| **Load** | `/proc/loadavg` | Real load average (1/5/15 min) normalized to CPU count |
| **Disk Health** | `smartctl -jH` | S.M.A.R.T. health status and temperature per device |

Devices without S.M.A.R.T. support (e.g. SD cards) are automatically skipped.

### Enable the DaemonSet

```bash
helm install tinymon-operator tinymon-operator/tinymon-operator \
  --set tinymon.url=https://mon.example.com \
  --set tinymon.apiKey=your-push-api-key \
  --set tinymon.clusterName=my-cluster \
  --set nodeMonitor.enabled=true
```

The DaemonSet runs a lightweight Alpine container with curl, jq, and smartmontools. It requires `privileged: true` for S.M.A.R.T. access and mounts the host root filesystem read-only at /host.

When the DaemonSet is active, it pushes more accurate values than the operator's Metrics API fallback. Both can run simultaneously -- the DaemonSet's newer results take precedence.

## Annotations

| Annotation | Description | Default | Resources |
|-----------|-------------|---------|-----------|
| `tinymon.io/enabled` | Enable monitoring ("true" to activate) | - | All |
| `tinymon.io/name` | Display name in TinyMon | Resource name | All |
| `tinymon.io/topic` | Topic/group in TinyMon | Kubernetes/cluster/kind/namespace | All |
| `tinymon.io/check-interval` | Check interval in seconds (minimum 30) | 60 (Ingress HTTP: 300, Certificate: 3600) | All |
| `tinymon.io/expected-status` | Expected HTTP status code for Ingress checks | 200 | Ingress |
| `tinymon.io/http-path` | Path to append to HTTP check URL (e.g. /docs) | / (root) | Ingress |
| `tinymon.io/icecast-mounts` | Comma-separated Icecast mountpoints | - | Ingress |

## Installation

### Prerequisites

- Kubernetes cluster with [metrics-server](https://github.com/kubernetes-sigs/metrics-server) installed (required for Node CPU/memory checks)
- [K8up](https://k8up.io) installed (only if monitoring K8up backup schedules)

### Helm

```bash
helm repo add tinymon-operator https://unclesamwk.github.io/tinymon-operator
helm repo update
helm install tinymon-operator tinymon-operator/tinymon-operator \
  --set tinymon.url=https://mon.example.com \
  --set tinymon.apiKey=your-push-api-key \
  --set tinymon.clusterName=my-cluster
```

### Examples

**Deployment monitoring:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  annotations:
    tinymon.io/enabled: "true"
    tinymon.io/name: "My Application"
    tinymon.io/topic: "production/apps"
    tinymon.io/check-interval: "120"
spec:
  replicas: 3
  # ...
```

Creates a host k8s://my-cluster/deployments/default/my-app with a status check that reports replica readiness every 120 seconds.

**Ingress with custom path and expected status:**

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: api
  annotations:
    tinymon.io/enabled: "true"
    tinymon.io/http-path: "/docs"
    tinymon.io/expected-status: "200"
spec:
  rules:
    - host: api.example.com
      # ...
```

Creates an HTTP check for https://api.example.com/docs expecting status 200, plus a certificate check.

**Ingress with Icecast listener monitoring:**

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: icecast
  annotations:
    tinymon.io/enabled: "true"
    tinymon.io/icecast-mounts: "/stream,/live"
spec:
  rules:
    - host: radio.example.com
      # ...
```

Creates icecast_listeners checks for each mountpoint in addition to the HTTP and certificate checks.

## Configuration

| Helm Value | Description | Default |
|-----------|-------------|--------|
| `tinymon.url` | TinyMon instance URL | (required) |
| `tinymon.apiKey` | Push API key | (required) |
| `tinymon.clusterName` | Cluster name used in addresses and topics | (required) |
| `image.repository` | Operator image | unclesamwk/tinymon-operator |
| `image.tag` | Image tag | appVersion |
| `nodeMonitor.enabled` | Enable Node Monitor DaemonSet | false |
| `nodeMonitor.image.repository` | Node Monitor image | unclesamwk/tinymon-node-monitor |
| `nodeMonitor.image.tag` | Node Monitor image tag | appVersion |
| `nodeMonitor.interval` | Collection interval in seconds | 60 |
| `nodeMonitor.resources` | Resource requests/limits for DaemonSet pods | 10m-50m CPU, 16-32Mi memory |

## RBAC

The operator requires the following cluster-level permissions:

| API Group | Resources | Verbs |
|-----------|-----------|-------|
| "" | nodes, persistentvolumeclaims | get, list, watch |
| apps | deployments | get, list, watch |
| networking.k8s.io | ingresses | get, list, watch |
| k8up.io | schedules, backups | get, list, watch |
| metrics.k8s.io | nodes | get, list |

## How It Works

The operator uses controller-runtime to watch Kubernetes resources. When a resource with `tinymon.io/enabled: "true"` is created, updated, or deleted:

1. **Created/Updated**: Upserts a host and checks in TinyMon via the Push API, then pushes current status as check results (bulk). Re-reconciles periodically based on the check interval.
2. **Annotation removed**: Deletes the host from TinyMon (cascades to checks and results)
3. **Resource deleted**: Deletes the host from TinyMon

Each resource gets a unique address in the format `k8s://<cluster>/<kind>/<namespace>/<name>` (or `k8s://<cluster>/<kind>/<name>` for cluster-scoped resources like Nodes). Topics follow the hierarchy `Kubernetes/<cluster>/<kind>/<namespace>` for grouping in the TinyMon dashboard.

## Development

```bash
# Build
go build ./...

# Run locally (requires kubeconfig)
export TINYMON_URL=https://mon.example.com
export TINYMON_API_KEY=your-key
export CLUSTER_NAME=my-cluster
go run .
```

## Contributing

Ideas, bug reports, and pull requests are welcome. Open an issue on GitHub if you have suggestions for new resource types, check improvements, or anything else that would make the operator more useful.

## License

[MIT](LICENSE)
