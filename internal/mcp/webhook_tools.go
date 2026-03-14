package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/synapbus/synapbus/internal/k8s"
	"github.com/synapbus/synapbus/internal/webhooks"
)

// WebhookToolRegistrar registers webhook and K8s handler MCP tools.
type WebhookToolRegistrar struct {
	webhookService *webhooks.WebhookService
	k8sService     *k8s.K8sService
	logger         *slog.Logger
}

// NewWebhookToolRegistrar creates a new webhook tool registrar.
func NewWebhookToolRegistrar(webhookService *webhooks.WebhookService, k8sService *k8s.K8sService) *WebhookToolRegistrar {
	return &WebhookToolRegistrar{
		webhookService: webhookService,
		k8sService:     k8sService,
		logger:         slog.Default().With("component", "mcp-webhook-tools"),
	}
}

// RegisterAll registers all webhook and K8s handler tools on the MCP server.
func (r *WebhookToolRegistrar) RegisterAll(s *server.MCPServer) {
	count := 0

	// Webhook tools
	if r.webhookService != nil {
		s.AddTool(r.registerWebhookTool(), r.handleRegisterWebhook)
		s.AddTool(r.listWebhooksTool(), r.handleListWebhooks)
		s.AddTool(r.deleteWebhookTool(), r.handleDeleteWebhook)
		count += 3
	}

	// K8s handler tools
	if r.k8sService != nil {
		s.AddTool(r.registerK8sHandlerTool(), r.handleRegisterK8sHandler)
		s.AddTool(r.listK8sHandlersTool(), r.handleListK8sHandlers)
		s.AddTool(r.deleteK8sHandlerTool(), r.handleDeleteK8sHandler)
		count += 3
	}

	r.logger.Info("webhook/K8s MCP tools registered", "count", count)
}

// --- Webhook Tool Definitions ---

func (r *WebhookToolRegistrar) registerWebhookTool() mcp.Tool {
	return mcp.NewTool("register_webhook",
		mcp.WithDescription("Register a webhook URL to receive event notifications. When matching events occur (messages, mentions), SynapBus will POST a signed JSON payload to your URL. Max 3 webhooks per agent. HTTPS required in production."),
		mcp.WithString("url", mcp.Description("HTTPS URL to receive webhook POST requests"), mcp.Required()),
		mcp.WithString("events", mcp.Description("Comma-separated event types: message.received, message.mentioned, channel.message"), mcp.Required()),
		mcp.WithString("secret", mcp.Description("Shared secret for HMAC-SHA256 payload signing (X-SynapBus-Signature header)"), mcp.Required()),
	)
}

func (r *WebhookToolRegistrar) listWebhooksTool() mcp.Tool {
	return mcp.NewTool("list_webhooks",
		mcp.WithDescription("List your registered webhooks and their status (active/disabled, failure counts)."),
	)
}

func (r *WebhookToolRegistrar) deleteWebhookTool() mcp.Tool {
	return mcp.NewTool("delete_webhook",
		mcp.WithDescription("Delete one of your registered webhooks by ID."),
		mcp.WithNumber("webhook_id", mcp.Description("ID of the webhook to delete"), mcp.Required()),
	)
}

// --- Webhook Tool Handlers ---

func (r *WebhookToolRegistrar) handleRegisterWebhook(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	url := req.GetString("url", "")
	eventsStr := req.GetString("events", "")
	secret := req.GetString("secret", "")

	if url == "" {
		return mcp.NewToolResultError("'url' parameter is required"), nil
	}
	if eventsStr == "" {
		return mcp.NewToolResultError("'events' parameter is required"), nil
	}
	if secret == "" {
		return mcp.NewToolResultError("'secret' parameter is required"), nil
	}

	// Parse comma-separated events
	events := parseEvents(eventsStr)

	wh, err := r.webhookService.RegisterWebhook(ctx, agentName, url, events, secret)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("register_webhook failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"webhook_id": wh.ID,
		"url":        wh.URL,
		"events":     wh.Events,
		"status":     wh.Status,
	})
}

func (r *WebhookToolRegistrar) handleListWebhooks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	hooks, err := r.webhookService.ListWebhooks(ctx, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list_webhooks failed: %s", err)), nil
	}

	result := make([]map[string]any, len(hooks))
	for i, wh := range hooks {
		result[i] = map[string]any{
			"id":                     wh.ID,
			"url":                    wh.URL,
			"events":                 wh.Events,
			"status":                 wh.Status,
			"consecutive_failures":   wh.ConsecutiveFailures,
			"created_at":             wh.CreatedAt,
		}
	}

	return resultJSON(map[string]any{
		"webhooks": result,
		"count":    len(result),
	})
}

func (r *WebhookToolRegistrar) handleDeleteWebhook(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	webhookID, err := req.RequireInt("webhook_id")
	if err != nil {
		return mcp.NewToolResultError("'webhook_id' parameter is required"), nil
	}

	if err := r.webhookService.DeleteWebhook(ctx, agentName, int64(webhookID)); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete_webhook failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"deleted":    true,
		"webhook_id": webhookID,
	})
}

// --- K8s Handler Tool Definitions ---

func (r *WebhookToolRegistrar) registerK8sHandlerTool() mcp.Tool {
	return mcp.NewTool("register_k8s_handler",
		mcp.WithDescription("Register a Kubernetes Job handler. When matching events occur, SynapBus launches a K8s Job with message data injected via environment variables. Only available when SynapBus runs in-cluster."),
		mcp.WithString("image", mcp.Description("Container image to run (e.g. myregistry/handler:v1)"), mcp.Required()),
		mcp.WithString("events", mcp.Description("Comma-separated event types: message.received, message.mentioned, channel.message"), mcp.Required()),
		mcp.WithString("namespace", mcp.Description("Kubernetes namespace (default: SynapBus's namespace)")),
		mcp.WithString("resources_memory", mcp.Description("Memory limit (e.g. 256Mi, 1Gi)")),
		mcp.WithString("resources_cpu", mcp.Description("CPU limit (e.g. 100m, 1)")),
		mcp.WithString("env", mcp.Description("Comma-separated KEY=VALUE environment variables")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Job timeout in seconds (default 300)")),
	)
}

func (r *WebhookToolRegistrar) listK8sHandlersTool() mcp.Tool {
	return mcp.NewTool("list_k8s_handlers",
		mcp.WithDescription("List your registered Kubernetes Job handlers and their status."),
	)
}

func (r *WebhookToolRegistrar) deleteK8sHandlerTool() mcp.Tool {
	return mcp.NewTool("delete_k8s_handler",
		mcp.WithDescription("Delete one of your registered Kubernetes Job handlers by ID."),
		mcp.WithNumber("handler_id", mcp.Description("ID of the K8s handler to delete"), mcp.Required()),
	)
}

// --- K8s Handler Tool Handlers ---

func (r *WebhookToolRegistrar) handleRegisterK8sHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	image := req.GetString("image", "")
	eventsStr := req.GetString("events", "")

	if image == "" {
		return mcp.NewToolResultError("'image' parameter is required"), nil
	}
	if eventsStr == "" {
		return mcp.NewToolResultError("'events' parameter is required"), nil
	}

	events := parseEvents(eventsStr)

	// Parse env vars
	envMap := make(map[string]string)
	if envStr := req.GetString("env", ""); envStr != "" {
		for _, pair := range strings.Split(envStr, ",") {
			pair = strings.TrimSpace(pair)
			if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
				envMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	handlerReq := k8s.RegisterHandlerRequest{
		Image:           image,
		Events:          events,
		Namespace:       req.GetString("namespace", ""),
		ResourcesMemory: req.GetString("resources_memory", ""),
		ResourcesCPU:    req.GetString("resources_cpu", ""),
		Env:             envMap,
		TimeoutSeconds:  req.GetInt("timeout_seconds", 300),
	}

	handler, err := r.k8sService.RegisterHandler(ctx, agentName, handlerReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("register_k8s_handler failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"handler_id":       handler.ID,
		"image":            handler.Image,
		"events":           handler.Events,
		"namespace":        handler.Namespace,
		"timeout_seconds":  handler.TimeoutSeconds,
		"status":           handler.Status,
	})
}

func (r *WebhookToolRegistrar) handleListK8sHandlers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	handlers, err := r.k8sService.ListHandlers(ctx, agentName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list_k8s_handlers failed: %s", err)), nil
	}

	result := make([]map[string]any, len(handlers))
	for i, h := range handlers {
		result[i] = map[string]any{
			"id":              h.ID,
			"image":           h.Image,
			"events":          h.Events,
			"namespace":       h.Namespace,
			"resources_memory": h.ResourcesMemory,
			"resources_cpu":   h.ResourcesCPU,
			"timeout_seconds": h.TimeoutSeconds,
			"status":          h.Status,
			"created_at":      h.CreatedAt,
		}
	}

	return resultJSON(map[string]any{
		"handlers":     result,
		"count":        len(result),
		"k8s_available": r.k8sService.IsAvailable(),
	})
}

func (r *WebhookToolRegistrar) handleDeleteK8sHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, ok := extractAgentName(ctx)
	if !ok {
		return mcp.NewToolResultError("authentication required"), nil
	}

	handlerID, err := req.RequireInt("handler_id")
	if err != nil {
		return mcp.NewToolResultError("'handler_id' parameter is required"), nil
	}

	if err := r.k8sService.DeleteHandler(ctx, agentName, int64(handlerID)); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete_k8s_handler failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"deleted":    true,
		"handler_id": handlerID,
	})
}

// parseEvents splits a comma-separated event string into a trimmed slice.
func parseEvents(s string) []string {
	parts := strings.Split(s, ",")
	events := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			events = append(events, p)
		}
	}
	return events
}
