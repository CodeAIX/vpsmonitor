# vpsmonitor Hub Helm Chart

A Kubernetes Helm chart for deploying [vpsmonitor Hub](https://www.vpsmonitor.dev/) - a monitoring and alerting solution for systems, containers, and services.

## Overview

This Helm chart simplifies the deployment of vpsmonitor Hub in Kubernetes environments. vpsmonitor Hub is a centralized monitoring hub that collects and aggregates system metrics from multiple agents deployed across your infrastructure.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- At least 500Mi of persistent storage (configurable)

## Quick Start

### 1. Install the OCI Chart

```bash
helm install vpsmonitor-hub oci://ghcr.io/henrygd/vpsmonitor-charts/vpsmonitor-hub
```

Or with a custom values file:

```bash
helm install vpsmonitor-hub oci://ghcr.io/henrygd/vpsmonitor-charts/vpsmonitor-hub -f custom-values.yaml
```

### 2. Access vpsmonitor Hub

By default, vpsmonitor Hub is accessible at `http://vpsmonitor-hub:8090` within the cluster.

```bash
# Port forward to access locally
kubectl port-forward svc/vpsmonitor-hub 8090:8090
```

Then visit: `http://localhost:8090`

## Configuration

### Basic Configuration

Key configuration options in `values.yaml`:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `replicaCount` | `1` | Number of vpsmonitor Hub replicas |
| `image.repository` | `henrygd/vpsmonitor` | Container image repository |
| `image.tag` | Chart AppVersion (0.21.0) | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `service.port` | `8090` | Service port |
| `persistentVolumeClaim.enabled` | `true` | Enable persistent volume |
| `persistentVolumeClaim.size` | `500Mi` | PVC size |

### Installation with Custom Values

```bash
helm install vpsmonitor-hub ./vpsmonitor-hub \
  --set replicaCount=2 \
  --set persistentVolumeClaim.size=1Gi \
  --set service.type=LoadBalancer
```

Or create a custom values file:

```yaml
# custom-values.yaml
replicaCount: 2
service:
  type: LoadBalancer
persistentVolumeClaim:
  size: 1Gi
```

Then install:

```bash
helm install vpsmonitor-hub ./vpsmonitor-hub -f custom-values.yaml
```

## Advanced Configuration

### Ingress Configuration

Enable and configure Ingress for external access:

```yaml
ingress:
  enabled: true
  className: nginx  # or your ingress class
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  hosts:
    - host: vpsmonitor.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: vpsmonitor-tls
      hosts:
        - vpsmonitor.example.com
```

### Persistent Volume Configuration

To use an existing PersistentVolumeClaim:

```yaml
persistentVolumeClaim:
  enabled: true
  existingClaim: "my-existing-pvc"
```

Or to use a specific storage class:

```yaml
persistentVolumeClaim:
  enabled: true
  storageClass: "fast-ssd"
  size: 1Gi
```

### Resource Limits

Set CPU and memory limits:

```yaml
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 250m
    memory: 256Mi
```

### Autoscaling

Enable Horizontal Pod Autoscaler:

```yaml
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

### Node Selection

Schedule pods on specific nodes:

```yaml
nodeSelector:
  node-type: monitoring

tolerations:
  - key: "monitoring"
    operator: "Equal"
    value: "true"
    effect: "NoSchedule"
```

## Deployment Examples

### Production Setup

```yaml
replicaCount: 3
image:
  tag: "0.21.0"
service:
  type: LoadBalancer
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: vpsmonitor.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: vpsmonitor-tls
      hosts:
        - vpsmonitor.example.com
persistentVolumeClaim:
  enabled: true
  storageClass: "fast-ssd"
  size: 2Gi
resources:
  limits:
    cpu: 1000m
    memory: 1Gi
  requests:
    cpu: 500m
    memory: 512Mi
autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
  targetCPUUtilizationPercentage: 75
```

### Development/Test Setup

```yaml
replicaCount: 1
service:
  type: ClusterIP
persistentVolumeClaim:
  enabled: true
  size: 500Mi
resources:
  limits:
    cpu: 200m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

## Managing vpsmonitor Hub

### Upgrade

```bash
helm upgrade vpsmonitor-hub ./vpsmonitor-hub
```

### Check Status

```bash
# Get deployment status
kubectl get deployment vpsmonitor-hub
kubectl get pods -l app.kubernetes.io/name=vpsmonitor

# Get service info
kubectl get svc vpsmonitor-hub
```

### View Logs

```bash
kubectl logs -l app.kubernetes.io/name=vpsmonitor -f
```

### Access Pod Shell

```bash
kubectl exec -it <pod-name> -- sh
```

### Uninstall

```bash
helm uninstall vpsmonitor-hub
```

## Connecting vpsmonitor Agents

After deploying vpsmonitor Hub, you can connect vpsmonitor agents running on:
- Kubernetes nodes
- VM instances
- Bare metal servers
- Docker containers

Agents communicate with the Hub on port `8090`. Configure the agent with the Hub's address:

```
HUB_URL=http://vpsmonitor-hub.default.svc.cluster.local:8090
```

Or for external access, use the LoadBalancer IP/DNS or Ingress hostname.

## Troubleshooting

### Pod won't start

```bash
# Check pod status and events
kubectl describe pod <pod-name>
kubectl logs <pod-name>
```

### Persistent volume issues

```bash
# Check PVC status
kubectl get pvc
kubectl describe pvc vpsmonitor-hub
```

### Connection issues with agents

- Verify the service is accessible: `kubectl get svc vpsmonitor-hub`
- Check network policies aren't blocking traffic
- Ensure agents can resolve the Hub's DNS name
- Verify port `8090` is open on the service

### Storage full

Increase PVC size:

```bash
# Update the PVC size in values
helm upgrade vpsmonitor-hub ./charts/vpsmonitor-hub \
  --set persistentVolumeClaim.size=2Gi
```

## Security Considerations

- Use network policies to restrict traffic to vpsmonitor Hub
- Enable RBAC and pod security policies
- Use TLS/HTTPS via Ingress with cert-manager
- Regularly update the image to the latest version
- Consider running with read-only filesystem
- Use private container registries if applicable

## Persistence

By default, vpsmonitor Hub uses a PersistentVolumeClaim for data storage. Ensure your Kubernetes cluster has enough storage capacity and a default storage class configured.

## Support and Documentation

- **Project Homepage**: https://www.vpsmonitor.dev/
- **GitHub Repository**: https://github.com/CodeAIX/vpsmonitor
- **Chart Repository**: https://github.com/CodeAIX/vpsmonitor-kubernetes

## Chart Information

- **Chart Version**: 0.1.0
- **App Version**: 0.21.0
- **Kubernetes Version**: 1.19+
- **Maintainer**: cloudwithdan (nikoloskid@pm.me)

## License

Please refer to the main vpsmonitor project repository for license information.
