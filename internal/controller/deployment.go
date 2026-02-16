package controller

import (
	"context"
	"time"
	"fmt"

	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type DeploymentReconciler struct {
	client.Client
	TinyMon *tinymon.Client
	Cluster string
}

func SetupDeploymentReconciler(mgr ctrl.Manager, tm *tinymon.Client, cluster string) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Complete(&DeploymentReconciler{Client: mgr.GetClient(), TinyMon: tm, Cluster: cluster})
}

func (r *DeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("deployment", req.NamespacedName)

	var deploy appsv1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &deploy); err != nil {
		if errors.IsNotFound(err) {
			log.Info("deployment deleted, removing from TinyMon")
			addr := resourceAddress(r.Cluster, "deployment", req.Namespace, req.Name)
			_ = r.TinyMon.DeleteHost(addr)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(deploy.Annotations) {
		addr := resourceAddress(r.Cluster, "deployment", deploy.Namespace, deploy.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress(r.Cluster, "deployment", deploy.Namespace, deploy.Name)
	interval := checkInterval(deploy.Annotations, 60)
	t := defaultTopic(r.Cluster, deploy.Namespace, "deployments", deploy.Annotations)

	host := tinymon.Host{
		Name:        displayName(deploy.Annotations, deploy.Name),
		Address:     addr,
		Description: fmt.Sprintf("Deployment %s/%s", deploy.Namespace, deploy.Name),
		Topic:       t,
		Enabled:     1,
	}

	log.Info("syncing deployment to TinyMon", "address", addr)
	if err := r.TinyMon.UpsertHost(host); err != nil {
		log.Error(err, "failed to upsert host")
		return ctrl.Result{}, err
	}

	check := tinymon.Check{
		HostAddress:     addr,
		Type:            "status",
		IntervalSeconds: interval,
		Enabled:         1,
	}
	if err := r.TinyMon.UpsertCheck(check); err != nil {
		log.Error(err, "failed to upsert check")
		return ctrl.Result{}, err
	}

	status, msg := deploymentStatus(&deploy)
	results := []tinymon.Result{{
		HostAddress: addr,
		CheckType:   "status",
		Status:      status,
		Message:     msg,
	}}
	if err := r.TinyMon.PushBulk(results); err != nil {
		log.Error(err, "failed to push bulk results")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Duration(interval) * time.Second}, nil
}

func deploymentStatus(deploy *appsv1.Deployment) (string, string) {
	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	ready := deploy.Status.ReadyReplicas
	available := deploy.Status.AvailableReplicas

	if ready == desired && available == desired {
		return "ok", fmt.Sprintf("%d/%d replicas ready", ready, desired)
	}
	if ready == 0 {
		return "critical", fmt.Sprintf("0/%d replicas ready", desired)
	}
	return "warning", fmt.Sprintf("%d/%d replicas ready", ready, desired)
}
