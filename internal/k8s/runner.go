package k8s

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// JobRunner is the interface for creating and managing K8s Jobs.
type JobRunner interface {
	// IsAvailable returns true if the K8s runner is available (running in-cluster).
	IsAvailable() bool
	// CreateJob creates a K8s Job for the given handler and message.
	CreateJob(ctx context.Context, handler *K8sHandler, msg *JobMessage) (string, error)
	// GetJobLogs returns the logs for a completed Job.
	GetJobLogs(ctx context.Context, namespace, jobName string) (string, error)
	// GetNamespace returns the namespace SynapBus is running in.
	GetNamespace() string
}

// JobMessage contains the message data to inject into the K8s Job.
type JobMessage struct {
	MessageID int64
	FromAgent string
	Body      string
	Event     string
	Channel   string
	Timestamp string
}

// K8sJobRunner implements JobRunner using the K8s API.
type K8sJobRunner struct {
	clientset kubernetes.Interface
	namespace string
	logger    *slog.Logger
}

// NewJobRunner attempts to create a K8s runner using in-cluster config.
// If not running in a K8s cluster, returns a NoopRunner.
func NewJobRunner(logger *slog.Logger) JobRunner {
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Info("not running in Kubernetes cluster, K8s job runner disabled", "reason", err.Error())
		return NewNoopRunner()
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error("failed to create K8s client", "error", err)
		return NewNoopRunner()
	}

	// Detect current namespace
	ns := detectNamespace()

	logger.Info("Kubernetes job runner initialized", "namespace", ns)
	return &K8sJobRunner{
		clientset: clientset,
		namespace: ns,
		logger:    logger,
	}
}

// NewJobRunnerWithClient creates a K8s runner with a provided clientset (for testing).
func NewJobRunnerWithClient(clientset kubernetes.Interface, namespace string, logger *slog.Logger) *K8sJobRunner {
	return &K8sJobRunner{
		clientset: clientset,
		namespace: namespace,
		logger:    logger,
	}
}

func (r *K8sJobRunner) IsAvailable() bool {
	return true
}

// GetClientset returns the kubernetes clientset for direct API access (used by reactor poller).
func (r *K8sJobRunner) GetClientset() kubernetes.Interface {
	return r.clientset
}

func (r *K8sJobRunner) GetNamespace() string {
	return r.namespace
}

func (r *K8sJobRunner) CreateJob(ctx context.Context, handler *K8sHandler, msg *JobMessage) (string, error) {
	jobName := sanitizeJobName(fmt.Sprintf("synapbus-%s-%d", handler.AgentName, msg.MessageID))

	namespace := handler.Namespace
	if namespace == "" {
		namespace = r.namespace
	}

	// Build environment variables
	envVars := []corev1.EnvVar{
		{Name: "SYNAPBUS_MESSAGE_ID", Value: fmt.Sprintf("%d", msg.MessageID)},
		{Name: "SYNAPBUS_MESSAGE_BODY", Value: truncateBody(msg.Body, 32768)},
		{Name: "SYNAPBUS_FROM_AGENT", Value: msg.FromAgent},
		{Name: "SYNAPBUS_EVENT", Value: msg.Event},
		{Name: "SYNAPBUS_TIMESTAMP", Value: msg.Timestamp},
	}
	if msg.Channel != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "SYNAPBUS_CHANNEL", Value: msg.Channel})
	}

	// Add user-defined env vars
	for k, v := range handler.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// Build resource limits
	resourceLimits := corev1.ResourceList{}
	if handler.ResourcesMemory != "" {
		resourceLimits[corev1.ResourceMemory] = resource.MustParse(handler.ResourcesMemory)
	}
	if handler.ResourcesCPU != "" {
		resourceLimits[corev1.ResourceCPU] = resource.MustParse(handler.ResourcesCPU)
	}

	backoffLimit := int32(0)
	activeDeadline := int64(handler.TimeoutSeconds)
	ttlAfterFinished := int32(3600)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "synapbus",
				"synapbus.io/agent":            handler.AgentName,
				"synapbus.io/handler-id":       fmt.Sprintf("%d", handler.ID),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			ActiveDeadlineSeconds:   &activeDeadline,
			TTLSecondsAfterFinished: &ttlAfterFinished,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "handler",
							Image:           handler.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            handler.Args,
							Env:             envVars,
							VolumeMounts:    buildVolumeMounts(handler.VolumeMounts),
							Resources: corev1.ResourceRequirements{
								Limits: resourceLimits,
							},
						},
					},
					Volumes: buildVolumes(handler.Volumes),
				},
			},
		},
	}

	created, err := r.clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create K8s Job: %w", err)
	}

	r.logger.Info("K8s Job created",
		"job_name", created.Name,
		"namespace", namespace,
		"agent", handler.AgentName,
		"image", handler.Image,
	)

	return created.Name, nil
}

func (r *K8sJobRunner) GetJobLogs(ctx context.Context, namespace, jobName string) (string, error) {
	// Find pods for this job
	pods, err := r.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		return "", fmt.Errorf("list pods for job %s: %w", jobName, err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for job %s", jobName)
	}

	// Get logs from the first pod
	pod := pods.Items[0]
	logStream, err := r.clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("get logs for pod %s: %w", pod.Name, err)
	}
	defer logStream.Close()

	logs, err := io.ReadAll(io.LimitReader(logStream, 1<<20)) // 1MB limit
	if err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}

	return string(logs), nil
}

// detectNamespace reads the current namespace from the mounted service account.
func detectNamespace() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	return "default"
}

// sanitizeJobName ensures the job name is valid for Kubernetes (lowercase, max 63 chars, DNS-safe).
func sanitizeJobName(name string) string {
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, name)
	// Trim leading/trailing hyphens
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// buildVolumeMounts converts our VolumeMount type to K8s VolumeMounts.
func buildVolumeMounts(mounts []VolumeMount) []corev1.VolumeMount {
	if len(mounts) == 0 {
		return nil
	}
	var result []corev1.VolumeMount
	for _, m := range mounts {
		result = append(result, corev1.VolumeMount{
			Name:      m.Name,
			MountPath: m.MountPath,
			ReadOnly:  m.ReadOnly,
		})
	}
	return result
}

// buildVolumes converts our Volume type to K8s Volumes.
func buildVolumes(volumes []Volume) []corev1.Volume {
	if len(volumes) == 0 {
		return nil
	}
	var result []corev1.Volume
	for _, v := range volumes {
		vol := corev1.Volume{Name: v.Name}
		if v.HostPath != "" {
			hostPathType := corev1.HostPathDirectory
			vol.VolumeSource = corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: v.HostPath,
					Type: &hostPathType,
				},
			}
		} else if v.EmptyDir {
			vol.VolumeSource = corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			}
		}
		result = append(result, vol)
	}
	return result
}

// truncateBody truncates the message body to maxLen bytes.
func truncateBody(body string, maxLen int) string {
	if len(body) <= maxLen {
		return body
	}
	return body[:maxLen]
}
