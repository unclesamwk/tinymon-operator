package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type BackupReconciler struct {
	client.Client
	TinyMon *tinymon.Client
}

func SetupBackupReconciler(mgr ctrl.Manager, tm *tinymon.Client) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8upv1.Schedule{}).
		Complete(&BackupReconciler{Client: mgr.GetClient(), TinyMon: tm})
}

func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("schedule", req.NamespacedName)

	var schedule k8upv1.Schedule
	if err := r.Get(ctx, req.NamespacedName, &schedule); err != nil {
		if errors.IsNotFound(err) {
			log.Info("K8up Schedule deleted, removing from TinyMon")
			addr := resourceAddress("backup", req.Namespace, req.Name)
			if err := r.TinyMon.DeleteHost(addr); err != nil {
				log.Error(err, "failed to delete host from TinyMon")
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(schedule.Annotations) {
		addr := resourceAddress("backup", schedule.Namespace, schedule.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress("backup", schedule.Namespace, schedule.Name)
	defaultTopic := schedule.Namespace + "/backups"
	t := topic(schedule.Annotations)
	if t == "" {
		t = defaultTopic
	}

	host := tinymon.Host{
		Name:        displayName(schedule.Annotations, schedule.Name),
		Address:     addr,
		Description: fmt.Sprintf("K8up Schedule %s/%s", schedule.Namespace, schedule.Name),
		Topic:       t,
		Enabled:     1,
	}

	log.Info("syncing K8up Schedule to TinyMon", "address", addr)
	if err := r.TinyMon.UpsertHost(host); err != nil {
		log.Error(err, "failed to upsert host")
		return ctrl.Result{}, err
	}

	check := tinymon.Check{
		HostAddress:     addr,
		Type:            "ping",
		IntervalSeconds: 3600,
		Enabled:         1,
	}
	if err := r.TinyMon.UpsertCheck(check); err != nil {
		log.Error(err, "failed to upsert check")
		return ctrl.Result{}, err
	}

	status, msg := backupStatus(&schedule)
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

func backupStatus(schedule *k8upv1.Schedule) (string, string) {
	for _, cond := range schedule.Status.Conditions {
		if cond.Type == "Ready" {
			if cond.Status == "True" {
				return "ok", fmt.Sprintf("Schedule is ready (last transition: %s)", cond.LastTransitionTime.Format(time.RFC3339))
			}
			return "warning", fmt.Sprintf("Schedule not ready: %s", cond.Message)
		}
	}

	if schedule.CreationTimestamp.Time.After(time.Now().Add(-5 * time.Minute)) {
		return "ok", "Schedule recently created"
	}

	return "unknown", "No status conditions available"
}
