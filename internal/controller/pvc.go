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

type PVCReconciler struct {
	client.Client
	TinyMon *tinymon.Client
}

func SetupPVCReconciler(mgr ctrl.Manager, tm *tinymon.Client) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Complete(&PVCReconciler{Client: mgr.GetClient(), TinyMon: tm})
}

func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("pvc", req.NamespacedName)

	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PVC deleted, removing from TinyMon")
			addr := resourceAddress("pvc", req.Namespace, req.Name)
			if err := r.TinyMon.DeleteHost(addr); err != nil {
				log.Error(err, "failed to delete host from TinyMon")
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(pvc.Annotations) {
		addr := resourceAddress("pvc", pvc.Namespace, pvc.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress("pvc", pvc.Namespace, pvc.Name)
	defaultTopic := pvc.Namespace + "/storage"
	t := topic(pvc.Annotations)
	if t == "" {
		t = defaultTopic
	}

	size := ""
	if s, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		size = s.String()
	}

	host := tinymon.Host{
		Name:        displayName(pvc.Annotations, pvc.Name),
		Address:     addr,
		Description: fmt.Sprintf("PVC %s/%s (%s)", pvc.Namespace, pvc.Name, size),
		Topic:       t,
		Enabled:     1,
	}

	log.Info("syncing PVC to TinyMon", "address", addr)
	if err := r.TinyMon.UpsertHost(host); err != nil {
		log.Error(err, "failed to upsert host")
		return ctrl.Result{}, err
	}

	check := tinymon.Check{
		HostAddress:     addr,
		Type:            "ping",
		IntervalSeconds: 300,
		Enabled:         1,
	}
	if err := r.TinyMon.UpsertCheck(check); err != nil {
		log.Error(err, "failed to upsert check")
		return ctrl.Result{}, err
	}

	status, msg := pvcStatus(&pvc)
	result := tinymon.Result{
		HostAddress: addr,
		CheckType:   "ping",
		Status:      status,
		Message:     msg,
	}
	if err := r.TinyMon.PushResult(result); err != nil {
		log.Error(err, "failed to push result")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func pvcStatus(pvc *corev1.PersistentVolumeClaim) (string, string) {
	switch pvc.Status.Phase {
	case corev1.ClaimBound:
		return "ok", "PVC is Bound"
	case corev1.ClaimPending:
		return "warning", "PVC is Pending"
	case corev1.ClaimLost:
		return "critical", "PVC is Lost"
	default:
		return "unknown", fmt.Sprintf("PVC phase: %s", pvc.Status.Phase)
	}
}
