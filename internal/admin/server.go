// Package admin provides a Unix domain socket server for local administration.
package admin

import (
	"context"
	"database/sql"
	"log/slog"
	"net"

	"github.com/synapbus/synapbus/internal/agents"
	"github.com/synapbus/synapbus/internal/attachments"
	"github.com/synapbus/synapbus/internal/auth"
	"github.com/synapbus/synapbus/internal/channels"
	"github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/messaging"
	"github.com/synapbus/synapbus/internal/search"
	"github.com/synapbus/synapbus/internal/trace"
	"github.com/synapbus/synapbus/internal/webhooks"
)

// WebhookServiceProvider defines the webhook operations needed by the admin socket.
type WebhookServiceProvider interface {
	RegisterWebhook(ctx context.Context, agentName, url string, events []string, secret string) (*webhooks.Webhook, error)
	ListWebhooks(ctx context.Context, agentName string) ([]*webhooks.Webhook, error)
	DeleteWebhook(ctx context.Context, agentName string, webhookID int64) error
}

// K8sServiceProvider defines the K8s handler operations needed by the admin socket.
type K8sServiceProvider interface {
	RegisterHandler(ctx context.Context, agentName string, req k8s.RegisterHandlerRequest) (*k8s.K8sHandler, error)
	ListHandlers(ctx context.Context, agentName string) ([]*k8s.K8sHandler, error)
	DeleteHandler(ctx context.Context, agentName string, handlerID int64) error
}

// Services holds references to all services the admin socket can control.
type Services struct {
	Users             *auth.SQLiteUserStore
	Sessions          auth.SessionStore
	Agents            *agents.AgentService
	Messages          *messaging.MessagingService
	Channels          *channels.Service
	Traces            trace.TraceStore
	EmbeddingStore    *search.EmbeddingStore
	VectorIndex       *search.VectorIndex
	SearchService     *search.Service
	AttachmentService *attachments.Service
	WebhookService    WebhookServiceProvider
	K8sService        K8sServiceProvider
	DataDir           string
	RetentionWorker   RetentionStatusProvider

	// CoreMemoryStore is the per-(owner, agent) core memory store wired
	// in for feature 020 admin CLI commands (`synapbus memory core ...`).
	// May be nil — handlers report "core memory store not configured".
	CoreMemoryStore *messaging.CoreMemoryStore

	// DreamRun, when non-nil, dispatches a single consolidation job
	// bypassing the trigger check. Wired by main.go when the
	// consolidator worker is enabled. Closure form keeps the worker
	// internals out of the admin package's import graph.
	DreamRun func(ctx context.Context, ownerID, jobType string) (jobID int64, err error)

	// DreamRunN fans out N parallel consolidation jobs (via slot 0..N-1)
	// for one (owner, job_type). Used by `synapbus memory dream-run
	// --parallel N`. core_rewrite always coerces to N=1 server-side.
	DreamRunN func(ctx context.Context, ownerID, jobType string, parallel int) (jobIDs []int64, err error)

	// DefaultDreamParallel is consulted when the CLI request omits
	// --parallel. Sourced from MemoryConfig.DreamParallel.
	DefaultDreamParallel int
}

// RetentionStatusProvider provides retention status information.
type RetentionStatusProvider interface {
	Status() map[string]interface{}
}

// AdminServer is a Unix domain socket server for local administration.
type AdminServer struct {
	listener   net.Listener
	db         *sql.DB
	services   *Services
	logger     *slog.Logger
	socketPath string
	done       chan struct{}
}

// NewServer creates a new admin server bound to a Unix socket at {dataDir}/synapbus.sock.
// If socketPath is non-empty it overrides the default.
func NewServer(socketPath string, db *sql.DB, services *Services, logger *slog.Logger) *AdminServer {
	return &AdminServer{
		db:         db,
		services:   services,
		logger:     logger.With("component", "admin"),
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
}

// SocketPath returns the path to the Unix socket.
func (s *AdminServer) SocketPath() string {
	return s.socketPath
}
