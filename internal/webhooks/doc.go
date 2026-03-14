// Package webhooks implements the webhook delivery engine for SynapBus.
// It provides webhook registration, HMAC-signed delivery, SSRF protection,
// rate limiting, retry with exponential backoff, and dead letter management.
package webhooks
