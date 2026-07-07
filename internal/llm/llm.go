// Package llm is the vendor-agnostic seam for AI-assisted drafting and the
// AI Ticket Intelligence Panel (DESIGN/08 §8.8-8.9), mirroring the existing
// EventPublisher/WebhookDispatcher/WorkflowTrigger seam pattern in
// internal/service/interfaces.go: internal/service depends only on the
// Client interface below, never a concrete vendor SDK.
package llm

import "context"

// Message is a single chat turn, matching the role/content shape shared by
// every OpenAI-compatible chat completion API (OpenAI, Azure OpenAI, and
// self-hosted engines like Ollama/vLLM/LM Studio that speak the same wire format).
type Message struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}

// Client is the only thing internal/service depends on. Complete sends a
// chat-style conversation and returns the model's raw text reply.
type Client interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}
