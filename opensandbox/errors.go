package opensandbox

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

func responseStatusError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("%s: HTTP %d: %s", operation, response.StatusCode, message)
}
