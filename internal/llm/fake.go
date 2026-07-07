package llm

import "context"

// FakeClient is a canned-response test double, so aisummary/aidraft tests
// (and integration tests) can assert persistence/merge/lock/regenerate logic
// without hitting a real model - the LLM's output is non-deterministic, the
// surrounding plumbing must not be.
type FakeClient struct {
	Response string
	Err      error
	// Calls records every request made, for assertions on prompt content.
	Calls [][]Message
}

func (f *FakeClient) Complete(ctx context.Context, messages []Message) (string, error) {
	f.Calls = append(f.Calls, messages)
	if f.Err != nil {
		return "", f.Err
	}
	return f.Response, nil
}
