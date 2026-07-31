package controller

import (
	"strings"
	"testing"
	"time"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// backup builds a Backup whose Completed condition carries the given status and reason,
// which is how k8up actually reports an outcome: there is no "Failed" condition type,
// only Completed=True with a reason of Succeeded or something else.
func backup(age time.Duration, conds ...metav1.Condition) k8upv1.Backup {
	return k8upv1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
		},
		Status: k8upv1.Status{Conditions: conds},
	}
}

func completed(reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:    k8upv1.ConditionCompleted.String(),
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	}
}

func TestLastBackupStatus(t *testing.T) {
	cases := []struct {
		name       string
		backups    []k8upv1.Backup
		wantStatus string
		wantInMsg  string
	}{
		{
			// The papra regression: k8up reports a failed run as Completed=True with
			// reason Failed. Reading only the condition type called this "ok" for nine
			// days while no backup had ever succeeded.
			name: "failed run is critical, not ok",
			backups: []k8upv1.Backup{backup(6*time.Hour, completed(
				string(k8upv1.ReasonFailed),
				`"backup_backup-schedule-backup-b4zmf" has 0 succeeded, 1 failed, and 0 started jobs`,
			))},
			wantStatus: "critical",
			wantInMsg:  "0 succeeded, 1 failed",
		},
		{
			name:       "succeeded run is ok",
			backups:    []k8upv1.Backup{backup(2*time.Hour, completed(string(k8upv1.ReasonSucceeded), "job completed"))},
			wantStatus: "ok",
			wantInMsg:  "completed",
		},
		{
			name:       "succeeded but older than 48h is stale",
			backups:    []k8upv1.Backup{backup(72*time.Hour, completed(string(k8upv1.ReasonSucceeded), "job completed"))},
			wantStatus: "warning",
			wantInMsg:  "stale",
		},
		{
			name:       "no backups at all",
			backups:    nil,
			wantStatus: "warning",
			wantInMsg:  "No backups found",
		},
		{
			name:       "fresh run without a terminal condition is in progress",
			backups:    []k8upv1.Backup{backup(5 * time.Minute)},
			wantStatus: "ok",
			wantInMsg:  "in progress",
		},
		{
			name:       "stuck run without a terminal condition warns",
			backups:    []k8upv1.Backup{backup(72 * time.Hour)},
			wantStatus: "warning",
			wantInMsg:  "No recent backup",
		},
		{
			// Only the newest run decides. An old success must not mask a fresh failure.
			name: "newest run wins over an older success",
			backups: []k8upv1.Backup{
				backup(48*time.Hour, completed(string(k8upv1.ReasonSucceeded), "job completed")),
				backup(1*time.Hour, completed(string(k8upv1.ReasonFailed), "job has failed")),
			},
			wantStatus: "critical",
			wantInMsg:  "failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg, _ := lastBackupStatus(tc.backups)
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q (message: %s)", status, tc.wantStatus, msg)
			}
			if !strings.Contains(msg, tc.wantInMsg) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.wantInMsg)
			}
		})
	}
}

func TestCompletedMessageFallsBackWhenConditionMissing(t *testing.T) {
	got := completedMessage(k8upv1.Status{})
	if got == "" {
		t.Fatal("completedMessage returned an empty string; a critical alert would have no reason in it")
	}
}
