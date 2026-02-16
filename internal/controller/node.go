package controller

import (
	"context"
	"fmt"

	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type NodeReconciler struct {
	client.Client
	TinyMon *tinymon.Client
}

func SetupNodeReconciler(mgr ctrl.Manager, tm *tinymon.Client) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(&NodeReconciler{Client: mgr.GetClient(), TinyMon: tm})
}

func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("node", req.Name)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if errors.IsNotFound(err) {
			log.Info("node deleted, removing from TinyMon")
			addr := resourceAddress("node", "", req.Name)
			if err := r.TinyMon.DeleteHost(addr); err != nil {
				log.Error(err, "failed to delete host from TinyMon")
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(node.Annotations) {
		addr := resourceAddress("node", "", node.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress("node", "", node.Name)
	host := tinymon.Host{
		Name:        displayName(node.Annotations, node.Name),
		Address:     addr,
		Description: fmt.Sprintf("Kubernetes Node %s", node.Name),
		Topic:       topic(node.Annotations),
		Enabled:     1,
	}

	log.Info("syncing node to TinyMon", "address", addr)
	if err := r.TinyMon.UpsertHost(host); err != nil {
		log.Error(err, "failed to upsert host")
		return ctrl.Result{}, err
	}

	status := nodeStatus(&node)
	check := tinymon.Check{
		HostAddress:     addr,
		Type:            "ping",
		IntervalSeconds: 60,
		Enabled:         1,
	}
	if err := r.TinyMon.UpsertCheck(check); err != nil {
		log.Error(err, "failed to upsert check")
		return ctrl.Result{}, err
	}

	result := tinymon.Result{
		HostAddress: addr,
		CheckType:   "ping",
		Status:      status.status,
		Message:     status.message,
	}
	if err := r.TinyMon.PushResult(result); err != nil {
		log.Error(err, "failed to push result")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

type nodeStatusInfo struct {
	status  string
	message string
}

func nodeStatus(node *corev1.Node) nodeStatusInfo {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			if cond.Status == corev1.ConditionTrue {
				return nodeStatusInfo{status: "ok", message: "Node is Ready"}
			}
			return nodeStatusInfo{status: "critical", message: fmt.Sprintf("Node not ready: %s", cond.Message)}
		}
	}
	return nodeStatusInfo{status: "unknown", message: "Node status unknown"}
}
