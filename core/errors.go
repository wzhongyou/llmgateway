package core

import (
	"fmt"
	"strings"
)

// ProviderError is returned by all providers for structured error handling.
type ProviderError struct {
	Provider   string
	StatusCode int    // HTTP status code, 0 for network/parse errors
	Message    string
	Retryable  bool
	Cause      error // underlying error, may be nil
}

func (e *ProviderError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Provider, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Cause }

// MultiError aggregates failures from multiple providers.
type MultiError struct {
	Errors []error
}

func (m *MultiError) Error() string {
	if len(m.Errors) == 1 {
		return m.Errors[0].Error()
	}
	msgs := make([]string, len(m.Errors))
	for i, e := range m.Errors {
		msgs[i] = e.Error()
	}
	return "all providers failed: " + strings.Join(msgs, "; ")
}

func (m *MultiError) Unwrap() []error { return m.Errors }
