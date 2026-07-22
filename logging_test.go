package dashboard

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogger(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return attr
	}}))
	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	request := httptest.NewRequest(http.MethodPost, "/dashboard/sandboxes?source=test", nil)
	request.Header.Set("X-Request-ID", "request-123")
	request.Header.Set("User-Agent", "test-agent")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); got != "request-123" {
		t.Errorf("X-Request-ID = %q, want %q", got, "request-123")
	}
	logLine := output.String()
	for _, expected := range []string{
		"msg=\"http request\"",
		"request_id=request-123",
		"method=POST",
		"path=/dashboard/sandboxes",
		"query=\"source=test\"",
		"status=201",
		"bytes=7",
		"user_agent=test-agent",
	} {
		if !strings.Contains(logLine, expected) {
			t.Errorf("log output %q does not contain %q", logLine, expected)
		}
	}
}
