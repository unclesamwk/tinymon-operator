# TinyMon Operator

Kubernetes operator that watches cluster resources and syncs them to [TinyMon](https://github.com/unclesamwk/TinyMon) via the Push API.

Annotate your Kubernetes resources with `tinymon.io/enabled: "true"` and the operator will automatically create hosts and checks in TinyMon.

## Watched Resources

| Resource | Check Type | Status Mapping |
|----------|-----------|----------------|
| **Node** | ping | Ready = ok, NotReady = critical |
| **Deployment** | ping | All replicas ready = ok, partial = warning, none = critical |
| **Ingress** | http + certificate | HTTP check per host, TLS certificate check for TLS hosts |
| **PVC** | ping | Bound = ok, Pending = warning, Lost = critical |
| **K8up Schedule** | ping | Ready condition = ok/warning, no conditions = unknown |

## Annotations

| Annotation | Description | Default |
|-----------|-------------|----------|
| `tinymon.io/enabled` | Enable monitoring (`"true"` to activate) | - |
| `tinymon.io/name` | Display name in TinyMon | Resource name |
| `tinymon.io/topic` | Topic/group in TinyMon | `<namespace>/<type>` |

## Installation

### Helm

```bash
helm repo add tinymon-operator https://unclesamwk.github.io/tinymon-operator
helm repo update
helm install tinymon-operator tinymon-operator/tinymon-operator \
  --set tinymon.url=https://mon.example.com \
  --set tinymon.apiKey=your-push-api-key
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
spec:
  replicas: 3
  # ...
```

This creates a host in TinyMon with address `k8s://deployment/default/my-app` and a ping check that reports replica readiness.

## Configuration

| Helm Value | Description | Default |
|-----------|-------------|----------|
| `tinymon.url` | TinyMon instance URL | (required) |
| `tinymon.apiKey` | Push API key | (required) |
| `image.repository` | Operator image | `unclesamwk/tinymon-operator` |
| `image.tag` | Image tag | appVersion |

## How It Works

The operator uses controller-runtime to watch Kubernetes resources. When a resource with `tinymon.io/enabled: "true"` is created, updated, or deleted:

1. **Created/Updated**: Upserts a host and check in TinyMon via the Push API, then pushes the current status as a check result
2. **Annotation removed**: Deletes the host from TinyMon (cascades to checks and results)
3. **Resource deleted**: Deletes the host from TinyMon

Each resource gets a unique address in the format `k8s://<kind>/<namespace>/<name>` (or `k8s://<kind>/<name>` for cluster-scoped resources like Nodes).

## Development

```bash
# Build
go build ./...

# Run locally (requires kubeconfig)
export TINYMON_URL=https://mon.example.com
export TINYMON_API_KEY=your-key
go run .
```

## License

MIT
