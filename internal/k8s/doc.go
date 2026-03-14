// Package k8s implements the Kubernetes Job runner for SynapBus.
// When running in-cluster, it creates K8s Jobs in response to message events.
// When not in-cluster, it provides a no-op implementation.
package k8s
