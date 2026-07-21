// Package opensandbox provides lifecycle API operations and merged discovery
// across the lifecycle API, BatchSandbox CRDs, and Agent Sandbox CRDs. It uses
// Kubernetes API proxy requests so the same client works with a local
// kubeconfig and from inside a cluster.
package opensandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const apiKeyHeader = "OPEN-SANDBOX-API-KEY"

// Reader defines read-only OpenSandbox operations.
type Reader interface {
	ListSandboxes(context.Context) ([]Sandbox, error)
	ListSnapshots(context.Context) ([]Snapshot, error)
	GetSnapshot(context.Context, string) (Snapshot, error)
	ListSandboxNodeLoads(context.Context) ([]SandboxNodeLoad, error)
}

// Writer defines state-changing OpenSandbox operations.
type Writer interface {
	CreateSandbox(context.Context, CreateSandboxRequest) (Sandbox, error)
	DeleteSandbox(context.Context, Sandbox) error
	PauseSandbox(context.Context, string) error
	ResumeSandbox(context.Context, string) error
	CreateSnapshot(context.Context, string, string) (Snapshot, error)
	DeleteSnapshot(context.Context, string) error
}

// Terminal opens an interactive PTY WebSocket through OpenSandbox.
type Terminal interface {
	OpenPTY(context.Context, string) (*websocket.Conn, error)
}

// CommandRunner executes a command inside a sandbox through OpenSandbox.
type CommandRunner interface {
	RunCommand(context.Context, string, string) (CommandResult, error)
}

// CommandResult contains the collected output and exit status of a command.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Client combines read, write, terminal, and command operations with lifecycle cleanup.
type Client interface {
	Reader
	Writer
	Terminal
	CommandRunner
	Close() error
}

// Options identifies the OpenSandbox lifecycle service and API key secret.
type Options struct {
	Namespace         string
	WorkloadNamespace string
	ServiceName       string
	ServicePort       string
	APIKeySecretName  string
	APIKeySecretKey   string
	Logger            *slog.Logger
}

type client struct {
	httpClient *http.Client
	proxyURL   string
	options    Options
	logger     *slog.Logger
	close      func() error

	apiKeyMutex sync.Mutex
	apiKey      string
}

type kubeSecret struct {
	Data map[string]string `json:"data"`
}

// NewFromKubeconfig authenticates Kubernetes API proxy requests with a
// kubeconfig. It does not require kubectl or a separate proxy process.
func NewFromKubeconfig(kubeconfigPath string, options Options) (Client, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return newFromRESTConfig(config, options)
}

// NewInCluster authenticates Kubernetes API proxy requests with the pod's
// mounted service-account credentials.
func NewInCluster(options Options) (Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes config: %w", err)
	}
	return newFromRESTConfig(config, options)
}

func newFromRESTConfig(config *rest.Config, options Options) (Client, error) {
	config = rest.CopyConfig(config)
	config.Timeout = 90 * time.Second
	config.UserAgent = "osb-dashboard"

	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes HTTP client: %w", err)
	}
	closeClient := func() error {
		httpClient.CloseIdleConnections()
		return nil
	}
	return newClient(config.Host, httpClient, options, closeClient)
}

// NewFromProxy connects through an existing loopback Kubernetes API proxy.
// It is useful when the caller manages the proxy separately.
func NewFromProxy(proxyURL string, options Options) (Client, error) {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse Kubernetes proxy URL: %w", err)
	}
	if parsedURL.Scheme != "http" {
		return nil, errors.New("Kubernetes proxy URL must use http")
	}
	host := parsedURL.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("Kubernetes proxy URL must use a loopback address")
	}
	return newClient(proxyURL, &http.Client{Timeout: 90 * time.Second}, options, nil)
}

func newClient(proxyURL string, httpClient *http.Client, options Options, closeClient func() error) (Client, error) {
	options = options.withDefaults()
	if strings.TrimSpace(proxyURL) == "" {
		return nil, errors.New("Kubernetes proxy URL is required")
	}
	if httpClient == nil {
		return nil, errors.New("HTTP client is required")
	}
	if closeClient == nil {
		closeClient = func() error { return nil }
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &client{
		httpClient: httpClient,
		proxyURL:   strings.TrimRight(proxyURL, "/"),
		options:    options,
		logger:     logger,
		close:      closeClient,
	}, nil
}

func (options Options) withDefaults() Options {
	if options.Namespace == "" {
		options.Namespace = "opensandbox-system"
	}
	if options.WorkloadNamespace == "" {
		options.WorkloadNamespace = "opensandbox"
	}
	if options.ServiceName == "" {
		options.ServiceName = "opensandbox-server"
	}
	if options.ServicePort == "" {
		options.ServicePort = "http"
	}
	if options.APIKeySecretName == "" {
		options.APIKeySecretName = "opensandbox-api-key"
	}
	if options.APIKeySecretKey == "" {
		options.APIKeySecretKey = "api-key"
	}
	return options
}

func (client *client) Close() error {
	return client.close()
}

func (client *client) newAPIRequest(ctx context.Context, method, path string, bodyReader io.Reader) (*http.Request, error) {
	apiKey, err := client.loadAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.serviceProxyURL()+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create OpenSandbox request: %w", err)
	}
	request.Header.Set(apiKeyHeader, apiKey)
	return request, nil
}

func (client *client) loadAPIKey(ctx context.Context) (string, error) {
	client.apiKeyMutex.Lock()
	defer client.apiKeyMutex.Unlock()
	if client.apiKey != "" {
		return client.apiKey, nil
	}

	endpoint := fmt.Sprintf(
		"%s/api/v1/namespaces/%s/secrets/%s",
		client.proxyURL,
		url.PathEscape(client.options.Namespace),
		url.PathEscape(client.options.APIKeySecretName),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create API key request: %w", err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("read OpenSandbox API key secret: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", responseStatusError("read OpenSandbox API key secret", response)
	}

	var secret kubeSecret
	if err := json.NewDecoder(response.Body).Decode(&secret); err != nil {
		return "", fmt.Errorf("decode OpenSandbox API key secret: %w", err)
	}
	encodedAPIKey := secret.Data[client.options.APIKeySecretKey]
	decodedAPIKey, err := base64.StdEncoding.DecodeString(encodedAPIKey)
	if err != nil {
		return "", fmt.Errorf("decode OpenSandbox API key: %w", err)
	}
	if len(decodedAPIKey) == 0 {
		return "", errors.New("OpenSandbox API key secret is empty")
	}
	client.apiKey = string(decodedAPIKey)
	return client.apiKey, nil
}

func (client *client) logCall(
	ctx context.Context,
	system string,
	method string,
	path string,
	status int,
	startedAt time.Time,
	err error,
	extra ...slog.Attr,
) {
	level := slog.LevelInfo
	attributes := []slog.Attr{
		slog.String("system", system),
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.Duration("duration", time.Since(startedAt)),
	}
	if err != nil {
		level = slog.LevelError
		attributes = append(attributes, slog.Any("error", err))
	}
	attributes = append(attributes, extra...)
	client.logger.LogAttrs(ctx, level, "upstream request", attributes...)
}

func (client *client) serviceProxyURL() string {
	return fmt.Sprintf(
		"%s/api/v1/namespaces/%s/services/http:%s:%s/proxy",
		client.proxyURL,
		url.PathEscape(client.options.Namespace),
		url.PathEscape(client.options.ServiceName),
		url.PathEscape(client.options.ServicePort),
	)
}
