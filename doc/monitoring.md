# Monitoring and Observability Guide

This guide covers monitoring, metrics, and observability for the SR-IOV Network Operator.

## Overview

Effective monitoring of SR-IOV infrastructure helps ensure:
- Optimal network performance
- Early detection of hardware issues  
- Resource utilization tracking
- Compliance with SLA requirements

## Built-in Metrics

### Enabling Metrics Collection

Enable the metrics exporter feature gate:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: sriov-network-operator
spec:
  featureGates:
    metricsExporter: true
```

### Operator Metrics

The operator exposes metrics on port 8080 at `/metrics` endpoint:

#### Configuration Metrics
- `sriov_operator_reconcile_duration_seconds` - Time spent reconciling resources
- `sriov_operator_reconcile_errors_total` - Number of reconciliation errors
- `sriov_policy_apply_duration_seconds` - Time to apply node policies
- `sriov_node_state_sync_duration_seconds` - Node state synchronization time

#### Resource Metrics  
- `sriov_networks_total` - Total number of SriovNetwork resources
- `sriov_node_policies_total` - Total number of SriovNetworkNodePolicy resources
- `sriov_managed_nodes_total` - Number of nodes managed by operator
- `sriov_vf_allocation_ratio` - VF allocation percentage per node

### Device Plugin Metrics

Device plugin metrics are available on each node:

#### Resource Allocation
- `sriov_device_plugin_allocated_devices` - Number of allocated VFs per resource
- `sriov_device_plugin_total_devices` - Total VFs available per resource
- `sriov_device_plugin_unhealthy_devices` - Number of unhealthy VFs

#### Performance Metrics
- `sriov_vf_rx_packets_total` - VF receive packet count
- `sriov_vf_tx_packets_total` - VF transmit packet count
- `sriov_vf_rx_bytes_total` - VF receive byte count
- `sriov_vf_tx_bytes_total` - VF transmit byte count

## Prometheus Integration

### ServiceMonitor Configuration

Create ServiceMonitor for operator metrics:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: sriov-operator-metrics
  namespace: sriov-network-operator
  labels:
    app: sriov-network-operator
spec:
  selector:
    matchLabels:
      app: sriov-network-operator
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
    scheme: http
```

### ServiceMonitor for Device Plugin

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: sriov-device-plugin-metrics
  namespace: sriov-network-operator
spec:
  selector:
    matchLabels:
      app: sriov-device-plugin
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
    scheme: http
```

### Prometheus Rules

Create alerting rules for common issues:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: sriov-alerts
  namespace: sriov-network-operator
spec:
  groups:
  - name: sriov-operator
    rules:
    - alert: SriovOperatorDown
      expr: up{job="sriov-operator-metrics"} == 0
      for: 5m
      labels:
        severity: critical
      annotations:
        summary: "SR-IOV operator is down"
        description: "SR-IOV operator has been down for more than 5 minutes"
    
    - alert: SriovHighReconcileErrors
      expr: rate(sriov_operator_reconcile_errors_total[5m]) > 0.1
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "High SR-IOV reconcile error rate"
        description: "SR-IOV operator is experiencing high reconcile error rate: {{ $value }}/sec"
    
    - alert: SriovVfAllocationHigh
      expr: sriov_vf_allocation_ratio > 0.9
      for: 10m
      labels:
        severity: warning
      annotations:
        summary: "High VF allocation on node {{ $labels.node }}"
        description: "VF allocation is {{ $value }}% on node {{ $labels.node }}"
    
    - alert: SriovUnhealthyDevices
      expr: sriov_device_plugin_unhealthy_devices > 0
      for: 2m
      labels:
        severity: critical
      annotations:
        summary: "Unhealthy SR-IOV devices detected"
        description: "{{ $value }} unhealthy SR-IOV devices on node {{ $labels.node }}"
```

## Grafana Dashboards

### Operator Overview Dashboard

```json
{
  "dashboard": {
    "title": "SR-IOV Network Operator Overview",
    "panels": [
      {
        "title": "Managed Nodes",
        "type": "stat",
        "targets": [
          {
            "expr": "sriov_managed_nodes_total",
            "legendFormat": "Total Nodes"
          }
        ]
      },
      {
        "title": "Total Networks",
        "type": "stat", 
        "targets": [
          {
            "expr": "sriov_networks_total",
            "legendFormat": "Networks"
          }
        ]
      },
      {
        "title": "Reconcile Duration",
        "type": "graph",
        "targets": [
          {
            "expr": "histogram_quantile(0.95, rate(sriov_operator_reconcile_duration_seconds_bucket[5m]))",
            "legendFormat": "95th percentile"
          },
          {
            "expr": "histogram_quantile(0.50, rate(sriov_operator_reconcile_duration_seconds_bucket[5m]))",
            "legendFormat": "50th percentile"
          }
        ]
      },
      {
        "title": "Error Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(sriov_operator_reconcile_errors_total[5m])",
            "legendFormat": "Errors/sec"
          }
        ]
      }
    ]
  }
}
```

### Device Utilization Dashboard

```json
{
  "dashboard": {
    "title": "SR-IOV Device Utilization",
    "panels": [
      {
        "title": "VF Allocation by Node",
        "type": "graph",
        "targets": [
          {
            "expr": "sriov_vf_allocation_ratio",
            "legendFormat": "{{ node }}"
          }
        ],
        "yAxes": [
          {
            "max": 1,
            "min": 0,
            "unit": "percentunit"
          }
        ]
      },
      {
        "title": "Total VFs Available",
        "type": "graph",
        "targets": [
          {
            "expr": "sriov_device_plugin_total_devices",
            "legendFormat": "{{ node }} - {{ resource }}"
          }
        ]
      },
      {
        "title": "Allocated VFs",
        "type": "graph",
        "targets": [
          {
            "expr": "sriov_device_plugin_allocated_devices",
            "legendFormat": "{{ node }} - {{ resource }}"
          }
        ]
      }
    ]
  }
}
```

## Node-Level Monitoring

### Custom Metrics Collection

Deploy node exporter with SR-IOV specific collectors:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: sriov-node-exporter
  namespace: sriov-network-operator
spec:
  selector:
    matchLabels:
      app: sriov-node-exporter
  template:
    metadata:
      labels:
        app: sriov-node-exporter
    spec:
      hostNetwork: true
      hostPID: true
      containers:
      - name: node-exporter
        image: prom/node-exporter:latest
        args:
        - --path.procfs=/host/proc
        - --path.sysfs=/host/sys
        - --collector.filesystem.ignored-mount-points
        - ^/(dev|proc|sys|var/lib/docker/.+)($|/)
        - --collector.textfile.directory=/var/lib/node_exporter/textfile_collector
        ports:
        - containerPort: 9100
          hostPort: 9100
        volumeMounts:
        - name: proc
          mountPath: /host/proc
          readOnly: true
        - name: sys
          mountPath: /host/sys
          readOnly: true
        - name: textfile
          mountPath: /var/lib/node_exporter/textfile_collector
      - name: sriov-collector
        image: busybox
        command: ["/bin/sh"]
        args:
        - -c
        - |
          while true; do
            # Collect SR-IOV specific metrics
            for dev in /sys/class/net/*/device/sriov_totalvfs; do
              if [ -f "$dev" ]; then
                interface=$(echo $dev | cut -d'/' -f5)
                total_vfs=$(cat $dev)
                current_vfs=$(cat /sys/class/net/$interface/device/sriov_numvfs 2>/dev/null || echo 0)
                echo "sriov_total_vfs{interface=\"$interface\"} $total_vfs" >> /tmp/sriov_metrics.prom.tmp
                echo "sriov_current_vfs{interface=\"$interface\"} $current_vfs" >> /tmp/sriov_metrics.prom.tmp
              fi
            done
            mv /tmp/sriov_metrics.prom.tmp /var/lib/node_exporter/textfile_collector/sriov_metrics.prom
            sleep 30
          done
        volumeMounts:
        - name: textfile
          mountPath: /var/lib/node_exporter/textfile_collector
        - name: sys
          mountPath: /sys
          readOnly: true
      volumes:
      - name: proc
        hostPath:
          path: /proc
      - name: sys
        hostPath:
          path: /sys
      - name: textfile
        emptyDir: {}
```

### Hardware Health Monitoring

Monitor SR-IOV hardware health:

```bash
#!/bin/bash
# Custom script for hardware health metrics

# Check interface link status
for iface in $(ls /sys/class/net/*/device/sriov_totalvfs | cut -d'/' -f5); do
    link_status=$(cat /sys/class/net/$iface/carrier 2>/dev/null || echo 0)
    echo "sriov_interface_link_up{interface=\"$iface\"} $link_status"
    
    # Check for hardware errors
    rx_errors=$(cat /sys/class/net/$iface/statistics/rx_errors 2>/dev/null || echo 0)
    tx_errors=$(cat /sys/class/net/$iface/statistics/tx_errors 2>/dev/null || echo 0)
    echo "sriov_interface_rx_errors_total{interface=\"$iface\"} $rx_errors"
    echo "sriov_interface_tx_errors_total{interface=\"$iface\"} $tx_errors"
done
```

## Application-Level Monitoring

### Pod Network Performance

Monitor application network performance:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: network-monitor
  annotations:
    k8s.v1.cni.cncf.io/networks: sriov-network
spec:
  containers:
  - name: monitor
    image: nicolaka/netshoot
    command: ["/bin/bash"]
    args:
    - -c
    - |
      while true; do
        # Monitor interface statistics
        for iface in $(ip link show | grep -E '^[0-9]+:.*net[0-9]+' | cut -d':' -f2 | tr -d ' '); do
          rx_bytes=$(cat /sys/class/net/$iface/statistics/rx_bytes)
          tx_bytes=$(cat /sys/class/net/$iface/statistics/tx_bytes)
          echo "$(date): $iface rx_bytes=$rx_bytes tx_bytes=$tx_bytes"
        done
        sleep 10
      done | tee /tmp/network_stats.log
```

### RDMA Performance Monitoring

For RDMA workloads:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: rdma-monitor
  annotations:
    k8s.v1.cni.cncf.io/networks: rdma-network
spec:
  containers:
  - name: rdma-monitor
    image: mellanox/rdma-tools
    command: ["/bin/bash"]
    args:
    - -c
    - |
      while true; do
        # Monitor RDMA statistics
        rdma statistic show
        # Monitor hardware counters
        for dev in $(rdma dev show | cut -d' ' -f1); do
          rdma statistic show link $dev
        done
        sleep 30
      done
```

## Log Aggregation

### Fluentd Configuration

Configure log collection for SR-IOV components:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluentd-sriov-config
  namespace: sriov-network-operator
data:
  fluent.conf: |
    <source>
      @type tail
      path /var/log/containers/sriov-network-operator-*.log
      pos_file /var/log/fluentd-sriov-operator.log.pos
      tag kubernetes.sriov.operator
      format json
      time_key time
      time_format %Y-%m-%dT%H:%M:%S.%NZ
    </source>
    
    <source>
      @type tail
      path /var/log/containers/sriov-config-daemon-*.log
      pos_file /var/log/fluentd-sriov-daemon.log.pos
      tag kubernetes.sriov.daemon
      format json
      time_key time
      time_format %Y-%m-%dT%H:%M:%S.%NZ
    </source>
    
    <filter kubernetes.sriov.**>
      @type kubernetes_metadata
    </filter>
    
    <match kubernetes.sriov.**>
      @type elasticsearch
      host elasticsearch.logging.svc.cluster.local
      port 9200
      index_name sriov-logs
    </match>
```

## Health Checks

### Operator Health

Create health check endpoints:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: sriov-operator-health
  namespace: sriov-network-operator
spec:
  selector:
    app: sriov-network-operator
  ports:
  - name: health
    port: 8081
    targetPort: 8081
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: sriov-operator-health
  namespace: sriov-network-operator
spec:
  selector:
    matchLabels:
      app: sriov-network-operator
  endpoints:
  - port: health
    path: /healthz
    interval: 30s
```

### Automated Health Checks

```bash
#!/bin/bash
# SR-IOV health check script

check_operator_health() {
    local status=$(kubectl get deployment sriov-network-operator -n sriov-network-operator -o jsonpath='{.status.readyReplicas}')
    if [ "$status" -eq 1 ]; then
        echo "PASS: Operator is running"
        return 0
    else
        echo "FAIL: Operator is not ready"
        return 1
    fi
}

check_node_states() {
    local failed_nodes=$(kubectl get sriovnetworknodestate -n sriov-network-operator -o json | jq -r '.items[] | select(.status.syncStatus != "Succeeded") | .metadata.name')
    if [ -z "$failed_nodes" ]; then
        echo "PASS: All nodes synchronized"
        return 0
    else
        echo "FAIL: Failed nodes: $failed_nodes"
        return 1
    fi
}

check_device_plugins() {
    local total_nodes=$(kubectl get nodes -l feature.node.kubernetes.io/network-sriov.capable=true --no-headers | wc -l)
    local ready_plugins=$(kubectl get pods -l app=sriov-device-plugin -n sriov-network-operator --field-selector=status.phase=Running --no-headers | wc -l)
    
    if [ "$total_nodes" -eq "$ready_plugins" ]; then
        echo "PASS: Device plugins running on all SR-IOV nodes"
        return 0
    else
        echo "FAIL: Device plugins: $ready_plugins/$total_nodes nodes"
        return 1
    fi
}

# Run all checks
checks=(check_operator_health check_node_states check_device_plugins)
failed=0

for check in "${checks[@]}"; do
    if ! $check; then
        failed=$((failed + 1))
    fi
done

if [ $failed -eq 0 ]; then
    echo "All health checks passed"
    exit 0
else
    echo "$failed health checks failed"
    exit 1
fi
```

## Performance Baselines

### Benchmark Collection

Establish performance baselines:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: sriov-baseline
spec:
  template:
    metadata:
      annotations:
        k8s.v1.cni.cncf.io/networks: sriov-network
    spec:
      containers:
      - name: iperf-server
        image: networkstatic/iperf3
        command: ["iperf3", "-s"]
        resources:
          requests:
            intel_sriov_netdevice: "1"
          limits:
            intel_sriov_netdevice: "1"
      - name: baseline-client
        image: networkstatic/iperf3
        command: ["/bin/bash"]
        args:
        - -c
        - |
          sleep 10  # Wait for server
          iperf3 -c localhost -t 60 -J > /tmp/baseline.json
          cat /tmp/baseline.json
      restartPolicy: Never
```

## Troubleshooting Monitoring

### Common Issues

1. **Metrics not appearing**: Check ServiceMonitor labels and selectors
2. **High memory usage**: Adjust metric retention and scrape intervals
3. **Missing node metrics**: Verify DaemonSet deployment and permissions
4. **Alert fatigue**: Tune thresholds and implement proper escalation

### Debug Metrics

```bash
# Check metric endpoints
kubectl port-forward -n sriov-network-operator deployment/sriov-network-operator 8080:8080
curl http://localhost:8080/metrics

# Verify ServiceMonitor
kubectl get servicemonitor -n sriov-network-operator -o yaml

# Check Prometheus targets
# Access Prometheus UI and verify SR-IOV targets are UP
```

## Best Practices

### Monitoring Strategy

1. **Layer monitoring**: Infrastructure, platform, and application levels
2. **Proactive alerting**: Set thresholds based on baselines
3. **Regular reviews**: Adjust monitoring based on operational experience
4. **Documentation**: Maintain runbooks for common scenarios

### Performance Optimization

1. **Metric cardinality**: Avoid high-cardinality labels
2. **Retention policies**: Balance history needs with storage costs  
3. **Aggregation**: Use recording rules for expensive queries
4. **Sampling**: Consider sampling for high-volume metrics

This monitoring guide provides comprehensive observability for SR-IOV infrastructure, enabling proactive management and performance optimization.