package k8s

import (
	"context"
	"fmt"
)

// NoopRunner is a no-op implementation of JobRunner for non-K8s environments.
type NoopRunner struct{}

// NewNoopRunner creates a new no-op runner.
func NewNoopRunner() *NoopRunner {
	return &NoopRunner{}
}

func (r *NoopRunner) IsAvailable() bool {
	return false
}

func (r *NoopRunner) GetNamespace() string {
	return ""
}

func (r *NoopRunner) CreateJob(ctx context.Context, handler *K8sHandler, msg *JobMessage) (string, error) {
	return "", fmt.Errorf("Kubernetes job runner is not available (not running in-cluster)")
}

func (r *NoopRunner) GetJobLogs(ctx context.Context, namespace, jobName string) (string, error) {
	return "", fmt.Errorf("Kubernetes job runner is not available (not running in-cluster)")
}
