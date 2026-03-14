package search

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/synapbus/synapbus/internal/search/embedding"
)

// Pipeline processes unembedded messages in the background.
type Pipeline struct {
	provider embedding.EmbeddingProvider
	store    *EmbeddingStore
	index    *VectorIndex
	config   Config
	logger   *slog.Logger
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewPipeline creates a new embedding pipeline.
func NewPipeline(
	provider embedding.EmbeddingProvider,
	store *EmbeddingStore,
	index *VectorIndex,
	config Config,
) *Pipeline {
	return &Pipeline{
		provider: provider,
		store:    store,
		index:    index,
		config:   config,
		logger:   slog.Default().With("component", "embedding-pipeline"),
	}
}

// Start launches the pipeline worker goroutines.
func (p *Pipeline) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)

	workers := p.config.WorkerCount
	if workers <= 0 {
		workers = 1
	}

	p.logger.Info("starting embedding pipeline", "workers", workers, "batch_size", p.config.BatchSize)

	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
}

// Stop shuts down the pipeline and waits for workers to finish.
func (p *Pipeline) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	p.logger.Info("embedding pipeline stopped")
}

// OnMessageCreated enqueues a new message for embedding.
func (p *Pipeline) OnMessageCreated(ctx context.Context, messageID int64, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	if err := p.store.Enqueue(ctx, messageID); err != nil {
		p.logger.Error("failed to enqueue message", "message_id", messageID, "error", err)
	}
}

// OnMessageDeleted removes a message's embedding.
func (p *Pipeline) OnMessageDeleted(ctx context.Context, messageID int64) {
	_ = p.store.DeleteEmbedding(ctx, messageID)
	p.index.Delete(messageID)
}

func (p *Pipeline) worker(ctx context.Context, workerID int) {
	defer p.wg.Done()

	logger := p.logger.With("worker", workerID)
	pollInterval := p.config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.processBatch(ctx, logger)
		}
	}
}

func (p *Pipeline) processBatch(ctx context.Context, logger *slog.Logger) {
	batchSize := p.config.BatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	items, err := p.store.Dequeue(ctx, batchSize)
	if err != nil {
		logger.Error("dequeue failed", "error", err)
		return
	}
	if len(items) == 0 {
		return
	}

	// Gather message bodies
	type msgData struct {
		item QueueItem
		body string
	}
	var batch []msgData

	for _, item := range items {
		body, err := p.store.GetMessageBody(ctx, item.MessageID)
		if err != nil {
			logger.Warn("message not found, marking completed", "message_id", item.MessageID, "error", err)
			_ = p.store.MarkCompleted(ctx, item.MessageID)
			continue
		}
		if strings.TrimSpace(body) == "" {
			_ = p.store.MarkCompleted(ctx, item.MessageID)
			continue
		}
		batch = append(batch, msgData{item: item, body: body})
	}

	if len(batch) == 0 {
		return
	}

	// Embed the batch
	texts := make([]string, len(batch))
	for i, m := range batch {
		texts[i] = m.body
	}

	vectors, err := p.provider.EmbedBatch(ctx, texts)
	if err != nil {
		logger.Error("embedding failed", "error", err, "batch_size", len(batch))

		// Mark all as failed for retry
		for _, m := range batch {
			errMsg := err.Error()
			_ = p.store.MarkFailed(ctx, m.item.MessageID, errMsg)
		}

		// Requeue failed items below max attempts
		if requeued, err := p.store.RequeueFailed(ctx, p.config.RetryMaxAttempts); err == nil && requeued > 0 {
			logger.Info("requeued failed items for retry", "count", requeued)
		}
		return
	}

	// Store results
	for i, m := range batch {
		if i >= len(vectors) || vectors[i] == nil {
			_ = p.store.MarkFailed(ctx, m.item.MessageID, "empty embedding returned")
			continue
		}

		// Add to HNSW index
		if err := p.index.AddVector(m.item.MessageID, vectors[i]); err != nil {
			logger.Error("add to index failed", "message_id", m.item.MessageID, "error", err)
			_ = p.store.MarkFailed(ctx, m.item.MessageID, err.Error())
			continue
		}

		// Record in SQLite
		if err := p.store.SaveEmbedding(ctx, m.item.MessageID, p.provider.Name(), p.provider.Name(), p.provider.Dimensions()); err != nil {
			logger.Error("save embedding record failed", "message_id", m.item.MessageID, "error", err)
			_ = p.store.MarkFailed(ctx, m.item.MessageID, err.Error())
			continue
		}

		_ = p.store.MarkCompleted(ctx, m.item.MessageID)
	}

	logger.Debug("batch processed", "count", len(batch))
}
