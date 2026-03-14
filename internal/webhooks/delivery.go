package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/synapbus/synapbus/internal/dispatcher"
)

// Retry intervals for exponential backoff.
var retryIntervals = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
}

const (
	maxAttempts    = 3
	defaultWorkers = 8
	queueSize      = 1000
)

// DeliveryPayload is the JSON body sent to webhook endpoints.
type DeliveryPayload struct {
	Event     string `json:"event"`
	MessageID int64  `json:"message_id"`
	FromAgent string `json:"from_agent"`
	ToAgent   string `json:"to_agent,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Body      string `json:"body"`
	Priority  int    `json:"priority"`
	Metadata  string `json:"metadata,omitempty"`
	Timestamp string `json:"timestamp"`
}

// DeliveryEngine implements dispatcher.EventDispatcher for webhooks.
// It maintains a worker pool for async delivery with retry support.
type DeliveryEngine struct {
	service     *WebhookService
	rateLimiter *AgentRateLimiter
	httpClient  *http.Client
	logger      *slog.Logger
	workers     int

	queue  chan *deliveryWork
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

type deliveryWork struct {
	delivery *WebhookDelivery
	webhook  *Webhook
	secret   string // raw secret (from registration) — we use SecretHash to sign
}

// NewDeliveryEngine creates a new delivery engine.
func NewDeliveryEngine(service *WebhookService, rateLimiter *AgentRateLimiter, allowPrivate bool) *DeliveryEngine {
	return &DeliveryEngine{
		service:     service,
		rateLimiter: rateLimiter,
		httpClient:  NewSSRFSafeClient(allowPrivate),
		logger:      slog.Default().With("component", "webhook-delivery"),
		workers:     defaultWorkers,
		queue:       make(chan *deliveryWork, queueSize),
	}
}

// Start launches the worker pool and re-queues any pending/retrying deliveries from a previous run.
func (e *DeliveryEngine) Start() {
	e.ctx, e.cancel = context.WithCancel(context.Background())
	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker(i)
	}
	// Start retry poller
	e.wg.Add(1)
	go e.retryPoller()
	// Start dead letter purge worker
	e.wg.Add(1)
	go e.deadLetterPurger()
	// Re-queue pending deliveries from previous run
	e.requeuePending()
	e.logger.Info("delivery engine started", "workers", e.workers)
}

// requeuePending re-queues deliveries that were pending or retrying when the server last stopped.
func (e *DeliveryEngine) requeuePending() {
	store := e.service.Store()
	ctx := e.ctx

	pending, err := store.GetPendingDeliveries(ctx, 100)
	if err != nil {
		e.logger.Error("failed to re-queue pending deliveries", "error", err)
		return
	}

	requeued := 0
	for _, d := range pending {
		wh, err := store.(*SQLiteWebhookStore).GetWebhookByID(ctx, d.WebhookID)
		if err != nil {
			continue
		}
		select {
		case e.queue <- &deliveryWork{delivery: d, webhook: wh}:
			requeued++
		default:
		}
	}

	if requeued > 0 {
		e.logger.Info("re-queued pending deliveries from previous run", "count", requeued)
	}
}

// deadLetterPurger periodically purges old dead-lettered deliveries (>30 days).
func (e *DeliveryEngine) deadLetterPurger() {
	defer e.wg.Done()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			store := e.service.Store()
			cutoff := time.Now().Add(-30 * 24 * time.Hour)
			count, err := store.PurgeOldDeadLetters(e.ctx, cutoff)
			if err != nil {
				e.logger.Error("failed to purge old dead letters", "error", err)
			} else if count > 0 {
				e.logger.Info("purged old dead letters", "count", count)
			}
		}
	}
}

// Stop gracefully shuts down the delivery engine.
func (e *DeliveryEngine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	e.logger.Info("delivery engine stopped")
}

// Dispatch implements dispatcher.EventDispatcher. It enqueues webhook deliveries
// for all active webhooks matching the event.
func (e *DeliveryEngine) Dispatch(ctx context.Context, event dispatcher.MessageEvent) error {
	// Check depth limit for loop prevention
	if event.Depth >= MaxDepth {
		e.logger.Warn("webhook chain depth exceeded, dropping event",
			"event_type", event.EventType,
			"message_id", event.MessageID,
			"depth", event.Depth,
		)
		return nil
	}

	// Find matching webhooks for the target agent
	targetAgent := event.ToAgent
	if targetAgent == "" && event.Channel != "" {
		// For channel messages, we don't deliver webhooks (channel messages go to
		// individual agents via mention events). But we handle message.mentioned
		// events which target specific agents.
		return nil
	}

	webhooks, err := e.service.GetActiveWebhooksForEvent(ctx, targetAgent, event.EventType)
	if err != nil {
		return fmt.Errorf("get webhooks for event: %w", err)
	}

	if len(webhooks) == 0 {
		return nil
	}

	// Build payload
	payload := DeliveryPayload{
		Event:     event.EventType,
		MessageID: event.MessageID,
		FromAgent: event.FromAgent,
		ToAgent:   event.ToAgent,
		Channel:   event.Channel,
		Body:      event.Body,
		Priority:  event.Priority,
		Metadata:  event.Metadata,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	store := e.service.Store()

	for _, wh := range webhooks {
		delivery := &WebhookDelivery{
			WebhookID:   wh.ID,
			AgentName:   wh.AgentName,
			Event:       event.EventType,
			MessageID:   event.MessageID,
			Payload:     string(payloadBytes),
			Status:      DeliveryStatusPending,
			MaxAttempts: maxAttempts,
			Depth:       event.Depth,
		}

		if _, err := store.InsertDelivery(ctx, delivery); err != nil {
			e.logger.Error("failed to insert delivery",
				"webhook_id", wh.ID,
				"error", err,
			)
			continue
		}

		// Enqueue for async delivery
		select {
		case e.queue <- &deliveryWork{delivery: delivery, webhook: wh}:
		default:
			e.logger.Warn("delivery queue full, delivery will be picked up by retry poller",
				"delivery_id", delivery.ID,
			)
		}
	}

	return nil
}

// DispatchMentions dispatches message.mentioned events for each @mentioned agent.
func (e *DeliveryEngine) DispatchMentions(ctx context.Context, event dispatcher.MessageEvent) error {
	for _, mentioned := range event.MentionedAgents {
		mentionEvent := dispatcher.MessageEvent{
			EventType: "message.mentioned",
			MessageID: event.MessageID,
			FromAgent: event.FromAgent,
			ToAgent:   mentioned,
			Channel:   event.Channel,
			Body:      event.Body,
			Priority:  event.Priority,
			Metadata:  event.Metadata,
			Depth:     event.Depth,
		}
		if err := e.Dispatch(ctx, mentionEvent); err != nil {
			e.logger.Error("failed to dispatch mention event",
				"mentioned", mentioned,
				"error", err,
			)
		}
	}
	return nil
}

func (e *DeliveryEngine) worker(id int) {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case work := <-e.queue:
			e.deliverOne(e.ctx, work)
		}
	}
}

func (e *DeliveryEngine) deliverOne(ctx context.Context, work *deliveryWork) {
	delivery := work.delivery
	wh := work.webhook
	store := e.service.Store()

	// Rate limit
	if err := e.rateLimiter.Wait(ctx, delivery.AgentName); err != nil {
		e.logger.Error("rate limiter error", "agent", delivery.AgentName, "error", err)
		return
	}

	// Increment attempt count
	delivery.Attempts++
	_ = store.UpdateDeliveryAttempts(ctx, delivery.ID, delivery.Attempts)

	// Build HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader([]byte(delivery.Payload)))
	if err != nil {
		e.recordFailure(ctx, delivery, wh, 0, fmt.Sprintf("build request: %s", err))
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SynapBus-Event", delivery.Event)
	req.Header.Set("X-SynapBus-Delivery", fmt.Sprintf("%d", delivery.ID))
	req.Header.Set("X-SynapBus-Depth", strconv.Itoa(delivery.Depth + 1))

	// HMAC signature using the stored secret hash as the key
	signature := ComputeHMACSignature([]byte(wh.SecretHash), []byte(delivery.Payload))
	req.Header.Set("X-SynapBus-Signature", signature)

	// Execute request
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.recordFailure(ctx, delivery, wh, 0, fmt.Sprintf("HTTP request: %s", err))
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Success
		now := time.Now()
		_ = store.UpdateDeliveryStatus(ctx, delivery.ID, DeliveryStatusDelivered, resp.StatusCode, "", nil, &now)
		_ = e.service.RecordSuccess(ctx, wh.ID)

		e.logger.Info("webhook delivered",
			"delivery_id", delivery.ID,
			"webhook_id", wh.ID,
			"status", resp.StatusCode,
		)
	} else {
		e.recordFailure(ctx, delivery, wh, resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
}

func (e *DeliveryEngine) recordFailure(ctx context.Context, delivery *WebhookDelivery, wh *Webhook, httpStatus int, errMsg string) {
	store := e.service.Store()

	if delivery.Attempts < delivery.MaxAttempts {
		// Schedule retry
		retryIdx := delivery.Attempts - 1
		if retryIdx >= len(retryIntervals) {
			retryIdx = len(retryIntervals) - 1
		}
		nextRetry := time.Now().Add(retryIntervals[retryIdx])
		_ = store.UpdateDeliveryStatus(ctx, delivery.ID, DeliveryStatusRetrying, httpStatus, errMsg, &nextRetry, nil)

		e.logger.Info("webhook delivery failed, scheduling retry",
			"delivery_id", delivery.ID,
			"attempt", delivery.Attempts,
			"next_retry", nextRetry,
			"error", errMsg,
		)
	} else {
		// Dead letter
		_ = store.UpdateDeliveryStatus(ctx, delivery.ID, DeliveryStatusDeadLettered, httpStatus, errMsg, nil, nil)
		_ = e.service.RecordFailure(ctx, wh.ID)

		e.logger.Warn("webhook delivery dead-lettered",
			"delivery_id", delivery.ID,
			"webhook_id", wh.ID,
			"attempts", delivery.Attempts,
			"error", errMsg,
		)
	}
}

func (e *DeliveryEngine) retryPoller() {
	defer e.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.processRetries()
		}
	}
}

func (e *DeliveryEngine) processRetries() {
	ctx := e.ctx
	store := e.service.Store()

	deliveries, err := store.GetRetryableDeliveries(ctx, time.Now(), 50)
	if err != nil {
		e.logger.Error("failed to get retryable deliveries", "error", err)
		return
	}

	for _, d := range deliveries {
		wh, err := store.(*SQLiteWebhookStore).GetWebhookByID(ctx, d.WebhookID)
		if err != nil {
			e.logger.Error("failed to get webhook for retry",
				"delivery_id", d.ID,
				"webhook_id", d.WebhookID,
				"error", err,
			)
			continue
		}

		select {
		case e.queue <- &deliveryWork{delivery: d, webhook: wh}:
		default:
			e.logger.Warn("delivery queue full during retry processing",
				"delivery_id", d.ID,
			)
		}
	}
}
