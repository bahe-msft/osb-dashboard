package opensandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	execdPort       = 44772
	ptyShellCommand = "env TERM=xterm-256color COLORTERM=truecolor sh -lc 'if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi'"
)

type createPTYSessionResponse struct {
	SessionID string `json:"session_id"`
}

type commandStreamNode struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Results  *struct {
		Text     string `json:"text"`
		ExitCode *int   `json:"exit_code,omitempty"`
	} `json:"results,omitempty"`
	Error *struct {
		Value string `json:"evalue"`
	} `json:"error,omitempty"`
}

// OpenPTY creates and attaches to an interactive PTY through the OpenSandbox
// server's WebSocket proxy.
func (client *client) OpenPTY(ctx context.Context, sandboxID string) (*websocket.Conn, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, errors.New("sandbox ID is required")
	}

	createPath := client.terminalPath(sandboxID, "/pty")
	startedAt := time.Now()
	requestBody, err := json.Marshal(map[string]string{"command": ptyShellCommand})
	if err != nil {
		return nil, fmt.Errorf("encode PTY session request: %w", err)
	}
	request, err := client.newAPIRequest(ctx, http.MethodPost, createPath, bytes.NewReader(requestBody))
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodPost, createPath, 0, startedAt, err)
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("create PTY session: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, createPath, 0, startedAt, requestErr)
		return nil, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		requestErr := responseStatusError("create PTY session", response)
		client.logCall(ctx, "opensandbox", http.MethodPost, createPath, response.StatusCode, startedAt, requestErr)
		return nil, requestErr
	}

	var payload createPTYSessionResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		decodeErr := fmt.Errorf("decode PTY session: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, createPath, response.StatusCode, startedAt, decodeErr)
		return nil, decodeErr
	}
	if payload.SessionID == "" {
		return nil, errors.New("OpenSandbox returned an empty PTY session ID")
	}
	client.logCall(ctx, "opensandbox", http.MethodPost, createPath, response.StatusCode, startedAt, nil)

	apiKey, err := client.loadAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	websocketPath := client.terminalPath(sandboxID, "/pty/"+url.PathEscape(payload.SessionID)+"/ws")
	websocketURL, err := url.Parse(client.serviceProxyURL() + websocketPath)
	if err != nil {
		return nil, fmt.Errorf("build PTY WebSocket URL: %w", err)
	}
	switch websocketURL.Scheme {
	case "http":
		websocketURL.Scheme = "ws"
	case "https":
		websocketURL.Scheme = "wss"
	default:
		return nil, fmt.Errorf("unsupported PTY WebSocket scheme %q", websocketURL.Scheme)
	}

	dialStartedAt := time.Now()
	connection, _, err := websocket.Dial(ctx, websocketURL.String(), &websocket.DialOptions{
		HTTPClient: client.httpClient,
		HTTPHeader: http.Header{apiKeyHeader: []string{apiKey}},
	})
	if err != nil {
		dialErr := fmt.Errorf("attach PTY WebSocket: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodGet, websocketPath, 0, dialStartedAt, dialErr)
		return nil, dialErr
	}
	connection.SetReadLimit(1 << 20)
	client.logCall(ctx, "opensandbox", http.MethodGet, websocketPath, http.StatusSwitchingProtocols, dialStartedAt, nil)
	return connection, nil
}

// RunCommand executes a command through the sandbox execd proxy and collects
// its streamed stdout, stderr, and exit status.
func (client *client) RunCommand(ctx context.Context, sandboxID, command string) (CommandResult, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return CommandResult{}, errors.New("sandbox ID is required")
	}
	if strings.TrimSpace(command) == "" {
		return CommandResult{}, errors.New("command is required")
	}

	commandPath := client.terminalPath(sandboxID, "/command")
	startedAt := time.Now()
	requestBody, err := json.Marshal(map[string]any{
		"command": command,
		"timeout": int64(5_000),
	})
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode command request: %w", err)
	}
	request, err := client.newAPIRequest(ctx, http.MethodPost, commandPath, bytes.NewReader(requestBody))
	if err != nil {
		client.logCall(ctx, "opensandbox", http.MethodPost, commandPath, 0, startedAt, err)
		return CommandResult{}, err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("run sandbox command: %w", err)
		client.logCall(ctx, "opensandbox", http.MethodPost, commandPath, 0, startedAt, requestErr)
		return CommandResult{}, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		requestErr := responseStatusError("run sandbox command", response)
		client.logCall(ctx, "opensandbox", http.MethodPost, commandPath, response.StatusCode, startedAt, requestErr)
		return CommandResult{}, requestErr
	}

	result, err := readCommandStream(response.Body)
	client.logCall(ctx, "opensandbox", http.MethodPost, commandPath, response.StatusCode, startedAt, err)
	if err != nil {
		return CommandResult{}, fmt.Errorf("read sandbox command output: %w", err)
	}
	return result, nil
}

func readCommandStream(reader io.Reader) (CommandResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var result CommandResult
	var event string
	var dataLines []string
	eventCount := 0
	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		eventCount++
		return appendCommandEvent(&result, event, data)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return CommandResult{}, err
			}
			event = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "{") {
			dataLines = append(dataLines, line)
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return CommandResult{}, err
	}
	if err := dispatch(); err != nil {
		return CommandResult{}, err
	}
	if eventCount == 0 {
		return CommandResult{}, errors.New("empty command stream")
	}
	return result, nil
}

func appendCommandEvent(result *CommandResult, event, data string) error {
	var node commandStreamNode
	if json.Unmarshal([]byte(data), &node) == nil {
		if node.Type != "" {
			event = node.Type
		}
		switch event {
		case "stdout":
			appendCommandText(&result.Stdout, node.Text)
		case "stderr":
			appendCommandText(&result.Stderr, node.Text)
		case "result":
			if node.Results != nil {
				appendCommandText(&result.Stdout, node.Results.Text)
				if node.Results.ExitCode != nil {
					result.ExitCode = *node.Results.ExitCode
				}
			}
			if node.ExitCode != nil {
				result.ExitCode = *node.ExitCode
			}
		case "error":
			if node.Error != nil && node.Error.Value != "" {
				exitCode, err := strconv.Atoi(node.Error.Value)
				if err != nil {
					return fmt.Errorf("parse command exit code %q: %w", node.Error.Value, err)
				}
				result.ExitCode = exitCode
			}
		}
		return nil
	}

	switch event {
	case "stdout":
		appendCommandText(&result.Stdout, data)
	case "stderr":
		appendCommandText(&result.Stderr, data)
	case "result":
		var payload struct {
			ExitCode int `json:"exit_code"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			result.ExitCode = payload.ExitCode
		}
	}
	return nil
}

func appendCommandText(destination *string, text string) {
	if text == "" {
		return
	}
	*destination += text
	if !strings.HasSuffix(text, "\n") {
		*destination += "\n"
	}
}

func (client *client) terminalPath(sandboxID, suffix string) string {
	return fmt.Sprintf(
		"/sandboxes/%s/proxy/%d%s",
		url.PathEscape(sandboxID),
		execdPort,
		suffix,
	)
}
