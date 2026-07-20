# Download pinned frontend dependencies required by the embedded dashboard.
prepare-web:
  ./scripts/fetch-ghostty-web.sh
  ./scripts/fetch-ui-assets.sh

# Run the development server with a kubeconfig for an OpenSandbox cluster.
dev kubeconfig: prepare-web
  go run . --kubeconfig "{{kubeconfig}}"

# Build the dashboard binary.
build: prepare-web
  go build -o osb-dashboard .

# Run unit and integration tests that do not require a live cluster.
test: prepare-web
  go test ./...

# Run the recorded Microsoft Edge E2E suite against an isolated live cluster.
e2e kubeconfig: prepare-web
  OSB_E2E_KUBECONFIG="{{kubeconfig}}" ./e2e/run.sh
