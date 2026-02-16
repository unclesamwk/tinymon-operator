package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type NodeReconciler struct {
	client.Client
	TinyMon   *tinymon.Client
	Cluster   string
	Clientset kubernetes.Interface
}

func SetupNodeReconciler(mgr ctrl.Manager, tm *tinymon.Client, cluster string, cs kubernetes.Interface) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(&NodeReconciler{Client: mgr.GetClient(), TinyMon: tm, Cluster: cluster, Clientset: cs})
}

func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("node", req.Name)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if errors.IsNotFound(err) {
			log.Info("node deleted, removing from TinyMon")
			addr := resourceAddress(r.Cluster, "node", "", req.Name)
			_ = r.TinyMon.DeleteHost(addr)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(node.Annotations) {
		addr := resourceAddress(r.Cluster, "node", "", node.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress(r.Cluster, "node", "", node.Name)
	interval := checkInterval(node.Annotations, 60)

	host := tinymon.Host{
		Name:        displayName(node.Annotations, node.Name),
		Address:     addr,
		Description: fmt.Sprintf("Kubernetes Node %s", node.Name),
		Topic:       defaultTopic(r.Cluster, "nodes", "", node.Annotations),
		Enabled:     1,
	}

	log.Info("syncing node to TinyMon", "address", addr)
	if err := r.TinyMon.UpsertHost(host); err != nil {
		log.Error(err, "failed to upsert host")
		return ctrl.Result{}, err
	}

	// Upsert checks: load, memory, disk
	for _, checkType := range []string{"load", "memory", "disk"} {
		check := tinymon.Check{
			HostAddress:     addr,
			Type:            checkType,
			IntervalSeconds: interval,
			Enabled:         1,
		}
		if err := r.TinyMon.UpsertCheck(check); err != nil {
			log.Error(err, "failed to upsert check", "type", checkType)
		}
	}

	// Get node metrics
	var results []tinymon.Result

	var nodeMetrics metricsv1beta1.NodeMetrics
	metricsErr := r.Get(ctx, client.ObjectKey{Name: node.Name}, &nodeMetrics)

	// Memory check
	if metricsErr == nil {
		usedMem := nodeMetrics.Usage.Memory().Value()
		allocMem := node.Status.Allocatable.Memory().Value()
		if allocMem > 0 {
			pct := float64(usedMem) / float64(allocMem) * 100
			status := thresholdStatus(pct)
			results = append(results, tinymon.Result{
				HostAddress: addr,
				CheckType:   "memory",
				Status:      status,
				Value:       pct,
				Message:     fmt.Sprintf("%.1f%% used (%s / %s)", pct, formatBytes(usedMem), formatBytes(allocMem)),
			})
		}
	} else {
		results = append(results, tinymon.Result{
			HostAddress: addr,
			CheckType:   "memory",
			Status:      "unknown",
			Message:     "Metrics API not available",
		})
	}

	// Load (CPU) check
	if metricsErr == nil {
		usedCPU := nodeMetrics.Usage.Cpu().MilliValue()
		allocCPU := node.Status.Allocatable.Cpu().MilliValue()
		if allocCPU > 0 {
			pct := float64(usedCPU) / float64(allocCPU) * 100
			status := thresholdStatus(pct)
			results = append(results, tinymon.Result{
				HostAddress: addr,
				CheckType:   "load",
				Status:      status,
				Value:       pct,
				Message:     fmt.Sprintf("%.1f%% CPU (%dm / %dm)", pct, usedCPU, allocCPU),
			})
		}
	} else {
		results = append(results, tinymon.Result{
			HostAddress: addr,
			CheckType:   "load",
			Status:      "unknown",
			Message:     "Metrics API not available",
		})
	}

	// Disk check via Kubelet Stats API
	diskResult := r.fetchDiskUsage(ctx, node.Name, addr)
	results = append(results, diskResult)

	if len(results) > 0 {
		if err := r.TinyMon.PushBulk(results); err != nil {
			log.Error(err, "failed to push bulk results")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: time.Duration(interval) * time.Second}, nil
}

// kubeletStatsSummary represents the relevant parts of /stats/summary
type kubeletStatsSummary struct {
	Node struct {
		Fs *struct {
			AvailableBytes *int64 `json:"availableBytes"`
			CapacityBytes  *int64 `json:"capacityBytes"`
			UsedBytes      *int64 `json:"usedBytes"`
		} `json:"fs"`
	} `json:"node"`
}

func (r *NodeReconciler) fetchDiskUsage(ctx context.Context, nodeName, addr string) tinymon.Result {
	raw, err := r.Clientset.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy", "stats", "summary").
		DoRaw(ctx)
	if err != nil {
		// Fallback: use DiskPressure condition
		return tinymon.Result{
			HostAddress: addr,
			CheckType:   "disk",
			Status:      "unknown",
			Message:     fmt.Sprintf("Kubelet Stats unavailable: %v", err),
		}
	}

	var stats kubeletStatsSummary
	if err := json.Unmarshal(raw, &stats); err != nil {
		return tinymon.Result{
			HostAddress: addr,
			CheckType:   "disk",
			Status:      "unknown",
			Message:     fmt.Sprintf("Failed to parse Kubelet Stats: %v", err),
		}
	}

	fs := stats.Node.Fs
	if fs == nil || fs.CapacityBytes == nil || fs.AvailableBytes == nil || *fs.CapacityBytes == 0 {
		return tinymon.Result{
			HostAddress: addr,
			CheckType:   "disk",
			Status:      "unknown",
			Message:     "No filesystem data in Kubelet Stats",
		}
	}

	usedBytes := *fs.CapacityBytes - *fs.AvailableBytes
	pct := float64(usedBytes) / float64(*fs.CapacityBytes) * 100
	status := thresholdStatus(pct)

	return tinymon.Result{
		HostAddress: addr,
		CheckType:   "disk",
		Status:      status,
		Value:       pct,
		Message:     fmt.Sprintf("%.1f%% used (%s / %s)", pct, formatBytes(usedBytes), formatBytes(*fs.CapacityBytes)),
	}
}

func thresholdStatus(pct float64) string {
	if pct >= 90 {
		return "critical"
	}
	if pct >= 80 {
		return "warning"
	}
	return "ok"
}

func formatBytes(b int64) string {
	const gi = 1024 * 1024 * 1024
	const mi = 1024 * 1024
	if b >= gi {
		return fmt.Sprintf("%.1f Gi", float64(b)/float64(gi))
	}
	return fmt.Sprintf("%.0f Mi", float64(b)/float64(mi))
}
