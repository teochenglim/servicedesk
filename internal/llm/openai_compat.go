package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPClient talks to any OpenAI-compatible /chat/completions endpoint -
// this one implementation covers OpenAI, Azure OpenAI, and self-hosted
// engines (Ollama, vLLM, LM Studio) since they all speak the same wire
// format; only BaseURL/APIKey/Model differ per deployment (internal/config).
// Defaults to a local Ollama instance, which needs no APIKey.
type HTTPClient struct {
	BaseURL string // e.g. "http://localhost:11434/v1" (Ollama) or "https://api.openai.com/v1"
	APIKey  string // "" for Ollama; required by most hosted providers
	Model   string
	HTTP    *http.Client
}

func NewHTTPClient(baseURL, apiKey, model string) *HTTPClient {
	return &HTTPClient{
		BaseURL: baseURL, APIKey: apiKey, Model: model,
		HTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *HTTPClient) Complete(ctx context.Context, messages []Message) (string, error) {
	body, err := json.Marshal(chatRequest{Model: c.Model, Messages: messages})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("llm: unparseable response (status %d): %s", resp.StatusCode, raw)
	}
	if out.Error != nil {
		return "", fmt.Errorf("llm: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: status %d: %s", resp.StatusCode, raw)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: response had no choices")
	}
	return out.Choices[0].Message.Content, nil
}
