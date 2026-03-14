package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/k8s"
)

// K8sHandler handles REST API requests for K8s handlers and job runs.
type K8sHandler struct {
	k8sService   *k8s.K8sService
	k8sStore     k8s.K8sStore
	agentService *agents.AgentService
	logger       *slog.Logger
}

// NewK8sHandler creates a new K8s handler.
func NewK8sHandler(ks *k8s.K8sService, store k8s.K8sStore, agentService *agents.AgentService) *K8sHandler {
	return &K8sHandler{
		k8sService:   ks,
		k8sStore:     store,
		agentService: agentService,
		logger:       slog.Default().With("component", "api.k8s"),
	}
}

// ListHandlers handles GET /api/k8s/handlers?agent={name}.
func (h *K8sHandler) ListHandlers(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	agentFilter := r.URL.Query().Get("agent")

	var allHandlers []*k8s.K8sHandler
	for _, agent := range ownedAgents {
		if agentFilter != "" && agent.Name != agentFilter {
			continue
		}
		handlers, err := h.k8sService.ListHandlers(r.Context(), agent.Name)
		if err != nil {
			h.logger.Error("list k8s handlers failed", "agent", agent.Name, "error", err)
			continue
		}
		allHandlers = append(allHandlers, handlers...)
	}

	if allHandlers == nil {
		allHandlers = []*k8s.K8sHandler{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"handlers": allHandlers,
		"total":    len(allHandlers),
	})
}

// ListJobRuns handles GET /api/k8s/job-runs?agent={name}&status={status}&limit={n}.
func (h *K8sHandler) ListJobRuns(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	ownedAgents, err := h.agentService.ListAgents(r.Context(), ownerID)
	if err != nil {
		h.logger.Error("list agents failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to list agents"))
		return
	}

	agentFilter := r.URL.Query().Get("agent")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	var allRuns []*k8s.K8sJobRun
	for _, agent := range ownedAgents {
		if agentFilter != "" && agent.Name != agentFilter {
			continue
		}
		runs, err := h.k8sService.GetJobRuns(r.Context(), agent.Name, status, limit)
		if err != nil {
			h.logger.Error("list job runs failed", "agent", agent.Name, "error", err)
			continue
		}
		allRuns = append(allRuns, runs...)
	}

	if allRuns == nil {
		allRuns = []*k8s.K8sJobRun{}
	}

	// Trim to limit
	if len(allRuns) > limit {
		allRuns = allRuns[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_runs": allRuns,
		"total":    len(allRuns),
	})
}

// JobRunLogs handles GET /api/k8s/job-runs/{id}/logs.
func (h *K8sHandler) JobRunLogs(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "Authentication required"))
		return
	}

	runID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_id", "Invalid job run ID"))
		return
	}

	run, err := h.k8sStore.GetJobRunByID(r.Context(), runID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("not_found", "Job run not found"))
		return
	}

	if !h.isAgentOwnedBy(r, run.AgentName, ownerID) {
		writeJSON(w, http.StatusForbidden, errorBody("forbidden", "You do not have access to this job run"))
		return
	}

	logs, err := h.k8sService.GetJobLogs(r.Context(), run.Namespace, run.JobName)
	if err != nil {
		h.logger.Error("get job logs failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("server_error", "Failed to get job logs"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_run": run,
		"logs":    logs,
	})
}

func (h *K8sHandler) isAgentOwnedBy(r *http.Request, agentName string, ownerID int64) bool {
	if agentName == "" {
		return false
	}
	agent, err := h.agentService.GetAgent(r.Context(), agentName)
	if err != nil {
		return false
	}
	return agent.OwnerID == ownerID
}
