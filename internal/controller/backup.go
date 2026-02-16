package controller

import (
	"context"
	"fmt"
	"sort"
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
			_ = r.TinyMon.DeleteHost(addr)
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
	interval := checkInterval(schedule.Annotations, 60)
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
		IntervalSeconds: interval,
		Enabled:         1,
	}
	if err := r.TinyMon.UpsertCheck(check); err != nil {
		log.Error(err, "failed to upsert check")
		return ctrl.Result{}, err
	}

	// List Backup objects in the same namespace
	var backupList k8upv1.BackupList
	if err := r.List(ctx, &backupList, client.InNamespace(schedule.Namespace)); err != nil {
		log.Error(err, "failed to list backups")
		results := []tinymon.Result{{
			HostAddress: addr,
			CheckType:   "ping",
			Status:      "unknown",
			Message:     "Failed to list backup objects",
		}}
		_ = r.TinyMon.PushBulk(results)
		return ctrl.Result{}, err
	}

	status, msg, ageSec := lastBackupStatus(backupList.Items)
	results := []tinymon.Result{{
		HostAddress: addr,
		CheckType:   "ping",
		Status:      status,
		Value:       ageSec,
		Message:     msg,
	}}
	if err := r.TinyMon.PushBulk(results); err != nil {
		log.Error(err, "failed to push bulk results")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func lastBackupStatus(backups []k8upv1.Backup) (string, string, float64) {
	if len(backups) == 0 {
		return "warning", "No backups found", 0
	}

	// Sort by creation time, newest first
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreationTimestamp.After(backups[j].CreationTimestamp.Time)
	})

	latest := backups[0]
	age := time.Since(latest.CreationTimestamp.Time)
	ageSec := age.Seconds()
	ageStr := formatDuration(age)

	// Check conditions for completion/failure
	for _, cond := range latest.Status.Conditions {
		if cond.Type == "Completed" && cond.Status == "True" {
			if age > 48*time.Hour {
				return "warning", fmt.Sprintf("Last backup completed %s ago (stale)", ageStr), ageSec
			}
			return "ok", fmt.Sprintf("Last backup completed %s ago", ageStr), ageSec
		}
		if cond.Type == "Failed" && cond.Status == "True" {
			return "critical", fmt.Sprintf("Last backup failed %s ago: %s", ageStr, cond.Message), ageSec
		}
	}

	// No terminal condition yet — might be running
	if age < 2*time.Hour {
		return "ok", fmt.Sprintf("Backup in progress (%s ago)", ageStr), ageSec
	}
	if age > 48*time.Hour {
		return "warning", fmt.Sprintf("No recent backup (last: %s ago)", ageStr), ageSec
	}

	return "ok", fmt.Sprintf("Last backup: %s ago", ageStr), ageSec
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
