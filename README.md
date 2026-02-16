# TinyMon Operator

Kubernetes operator that watches cluster resources and syncs them to [TinyMon](https://github.com/unclesamwk/TinyMon) via the Push API.

Annotate your Kubernetes resources with `tinymon.io/enabled: "true"` and the operator will automatically create hosts and checks in TinyMon.

## Watched Resources

| Resource | Check Types | Mode | Status Mapping |
|----------|------------|------|-----------------|
| **Node** | load, memory, disk | Push | CPU/Memory via Metrics API: ok <80%, warning 80-90%, critical >90%. Disk via DiskPressure condition. |
| **Deployment** | status | Push | All replicas ready = ok, partial = warning, none = critical |
| **Ingress** | http, certificate | Pull | Created in TinyMon, executed by TinyMon (not pushed by operator) |
| **PVC** | disk | Push | Bound = ok, Pending = warning, Lost = critical. Value: requested size in GB. |
| **K8up Schedule** | status | Push | Lists Backup objects: Completed = ok, Failed = critical, >48h stale = warning |

**Push**: The operator pushes check results to TinyMon via the Bulk API.
**Pull**: The operator only creates the checks in TinyMon. TinyMon executes them independently.

## Annotations

| Annotation | Description | Default |
|-----------|-------------|---------|
| `tinymon.io/enabled` | Enable monitoring (`"true"` to activate) | - |
| `tinymon.io/name` | Display name in TinyMon | Resource name |
| `tinymon.io/topic` | Topic/group in TinyMon | `Kubernetes/<cluster>/<kind>/<namespace>` |
| `tinymon.io/check-interval` | Check interval in seconds (minimum 30) | 60 (Ingress HTTP: 300, Certificate: 3600) |
| `tinymon.io/expected-status` | Expected HTTP status code for Ingress checks | 200 |

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

### Example

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

This creates a host in TinyMon with address `k8s://my-cluster/deployments/default/my-app` and a status check that reports replica readiness every 120 seconds.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-ingress
  annotations:
    tinymon.io/enabled: "true"
    tinymon.io/expected-status: "301"
spec:
  rules:
    - host: example.com
      # ...
```

This creates HTTP and certificate checks in TinyMon for each host in the Ingress. TinyMon executes these checks itself (pull mode). The expected HTTP status code is set to 301.

## Configuration

| Helm Value | Description | Default |
|-----------|-------------|--------|
| `tinymon.url` | TinyMon instance URL | (required) |
| `tinymon.apiKey` | Push API key | (required) |
| `tinymon.clusterName` | Cluster name used in addresses and topics | (required) |
| `image.repository` | Operator image | `unclesamwk/tinymon-operator` |
| `image.tag` | Image tag | appVersion |

## RBAC

The operator requires the following cluster-level permissions:

| API Group | Resources | Verbs |
|-----------|-----------|-------|
| `""` | nodes, persistentvolumeclaims | get, list, watch |
| `apps` | deployments | get, list, watch |
| `networking.k8s.io` | ingresses | get, list, watch |
| `k8up.io` | schedules, backups | get, list, watch |
| `metrics.k8s.io` | nodes | get, list |

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

MIT
