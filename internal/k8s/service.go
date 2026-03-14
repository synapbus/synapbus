package k8s

import (
	"context"
	"fmt"
	"log/slog"
)

const (
	// MaxHandlersPerAgent is the maximum number of K8s handlers per agent.
	MaxHandlersPerAgent = 3
	// HandlerStatusActive is the active handler status.
	HandlerStatusActive = "active"
	// HandlerStatusDisabled is the disabled handler status.
	HandlerStatusDisabled = "disabled"
)

// ValidK8sEvents are the valid event types for K8s handlers.
var ValidK8sEvents = []string{"message.received", "message.mentioned", "channel.message"}

// K8sService provides business logic for K8s handler operations.
type K8sService struct {
	store  K8sStore
	runner JobRunner
	logger *slog.Logger
}

// NewK8sService creates a new K8s handler service.
func NewK8sService(store K8sStore, runner JobRunner) *K8sService {
	return &K8sService{
		store:  store,
		runner: runner,
		logger: slog.Default().With("component", "k8s-service"),
	}
}

// RegisterHandler registers a new K8s handler for an agent.
func (s *K8sService) RegisterHandler(ctx context.Context, agentName string, req RegisterHandlerRequest) (*K8sHandler, error) {
	// Validate events
	for _, e := range req.Events {
		if !isValidK8sEvent(e) {
			return nil, fmt.Errorf("invalid event type: %q", e)
		}
	}
	if len(req.Events) == 0 {
		return nil, fmt.Errorf("at least one event type is required")
	}

	// Validate image
	if req.Image == "" {
		return nil, fmt.Errorf("image is required")
	}

	// Check K8s availability
	if !s.runner.IsAvailable() {
		return nil, fmt.Errorf("Kubernetes runner is not available (not running in-cluster)")
	}

	// Check max handlers
	count, err := s.store.CountHandlersByAgent(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("count handlers: %w", err)
	}
	if count >= MaxHandlersPerAgent {
		return nil, fmt.Errorf("maximum K8s handlers reached (%d/%d)", count, MaxHandlersPerAgent)
	}

	// Default timeout
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}

	// Default namespace
	namespace := req.Namespace
	if namespace == "" {
		namespace = s.runner.GetNamespace()
	}

	handler := &K8sHandler{
		AgentName:       agentName,
		Image:           req.Image,
		Events:          req.Events,
		Namespace:       namespace,
		ResourcesMemory: req.ResourcesMemory,
		ResourcesCPU:    req.ResourcesCPU,
		Env:             req.Env,
		TimeoutSeconds:  timeout,
		Status:          HandlerStatusActive,
	}

	id, err := s.store.InsertHandler(ctx, handler)
	if err != nil {
		return nil, fmt.Errorf("insert handler: %w", err)
	}

	s.logger.Info("K8s handler registered",
		"id", id,
		"agent", agentName,
		"image", req.Image,
		"events", req.Events,
	)

	return handler, nil
}

// ListHandlers returns all K8s handlers for an agent.
func (s *K8sService) ListHandlers(ctx context.Context, agentName string) ([]*K8sHandler, error) {
	return s.store.GetHandlersByAgent(ctx, agentName)
}

// DeleteHandler deletes a K8s handler owned by the agent.
func (s *K8sService) DeleteHandler(ctx context.Context, agentName string, handlerID int64) error {
	if err := s.store.DeleteHandler(ctx, handlerID, agentName); err != nil {
		return fmt.Errorf("delete handler: %w", err)
	}
	s.logger.Info("K8s handler deleted",
		"id", handlerID,
		"agent", agentName,
	)
	return nil
}

// GetJobRuns returns job runs for an agent, optionally filtered by status.
func (s *K8sService) GetJobRuns(ctx context.Context, agentName, status string, limit int) ([]*K8sJobRun, error) {
	return s.store.GetJobRunsByAgent(ctx, agentName, status, limit)
}

// GetJobLogs returns the logs for a completed K8s Job.
func (s *K8sService) GetJobLogs(ctx context.Context, namespace, jobName string) (string, error) {
	if !s.runner.IsAvailable() {
		return "", fmt.Errorf("Kubernetes runner is not available")
	}
	return s.runner.GetJobLogs(ctx, namespace, jobName)
}

// IsAvailable returns whether the K8s runner is available.
func (s *K8sService) IsAvailable() bool {
	return s.runner.IsAvailable()
}

// Store returns the underlying K8s store.
func (s *K8sService) Store() K8sStore {
	return s.store
}

// RegisterHandlerRequest contains the parameters for registering a K8s handler.
type RegisterHandlerRequest struct {
	Image           string            `json:"image"`
	Events          []string          `json:"events"`
	Namespace       string            `json:"namespace"`
	ResourcesMemory string            `json:"resources_memory"`
	ResourcesCPU    string            `json:"resources_cpu"`
	Env             map[string]string `json:"env"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
}

func isValidK8sEvent(event string) bool {
	for _, e := range ValidK8sEvents {
		if e == event {
			return true
		}
	}
	return false
}
