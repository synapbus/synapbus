package k8sjob

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ClientsetWaiter is a Waiter backed by a real k8s.io/client-go clientset.
// It polls Job status on an interval (default 10s) until the Job reports
// Complete, Failed, or the context is cancelled.
type ClientsetWaiter struct {
	Clientset kubernetes.Interface
	Interval  time.Duration
}

// NewClientsetWaiter constructs a Waiter from an existing clientset.
// Interval defaults to 10 seconds when zero.
func NewClientsetWaiter(cs kubernetes.Interface, interval time.Duration) *ClientsetWaiter {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &ClientsetWaiter{Clientset: cs, Interval: interval}
}

// Wait blocks until the named Job enters a terminal state or the context
// is cancelled. The returned JobOutcome has Success=true only when the
// Job's JobComplete condition is True.
func (w *ClientsetWaiter) Wait(ctx context.Context, namespace, jobName string) (JobOutcome, error) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	// Do an immediate first check so short-running jobs return fast in
	// tests (interval may be set to a small value).
	if outcome, done, err := w.check(ctx, namespace, jobName); done || err != nil {
		return outcome, err
	}

	for {
		select {
		case <-ctx.Done():
			return JobOutcome{}, ctx.Err()
		case <-ticker.C:
			outcome, done, err := w.check(ctx, namespace, jobName)
			if err != nil {
				return JobOutcome{}, err
			}
			if done {
				return outcome, nil
			}
		}
	}
}

func (w *ClientsetWaiter) check(ctx context.Context, namespace, jobName string) (JobOutcome, bool, error) {
	job, err := w.Clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		return JobOutcome{}, false, fmt.Errorf("get job %s/%s: %w", namespace, jobName, err)
	}
	for _, cond := range job.Status.Conditions {
		if cond.Status != "True" {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			return JobOutcome{Success: true}, true, nil
		case batchv1.JobFailed:
			reason := cond.Reason
			if cond.Message != "" {
				if reason != "" {
					reason += ": "
				}
				reason += cond.Message
			}
			return JobOutcome{Success: false, FailureReason: reason}, true, nil
		}
	}
	if job.Status.Failed > 0 {
		return JobOutcome{Success: false, FailureReason: "pod failure"}, true, nil
	}
	return JobOutcome{}, false, nil
}

// Cancel deletes the Job (and its pods) by name. Used by harness.Cancel.
func (w *ClientsetWaiter) Cancel(ctx context.Context, namespace, jobName string) error {
	propagation := metav1.DeletePropagationBackground
	err := w.Clientset.BatchV1().Jobs(namespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil {
		return fmt.Errorf("delete job %s/%s: %w", namespace, jobName, err)
	}
	return nil
}
