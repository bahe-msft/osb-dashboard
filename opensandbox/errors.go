package opensandbox

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPStatusError reports a non-successful response from OpenSandbox or Kubernetes.
type HTTPStatusError struct {
	Operation  string
	StatusCode int
	Message    string
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", err.Operation, err.StatusCode, err.Message)
}

// IsHTTPStatus reports whether err contains an HTTPStatusError with the given status.
func IsHTTPStatus(err error, status int) bool {
	var statusErr *HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == status
}

func responseStatusError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return &HTTPStatusError{Operation: operation, StatusCode: response.StatusCode, Message: message}
}
