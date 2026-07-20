# Download pinned frontend dependencies required by the embedded dashboard.
prepare-web:
  ./scripts/fetch-ghostty-web.sh

# Run the development server with a kubeconfig for an OpenSandbox cluster.
dev kubeconfig: prepare-web
  go run . --kubeconfig "{{kubeconfig}}"

# Build the dashboard binary.
build: prepare-web
  go build -o osb-dashboard .

# Run all tests.
test: prepare-web
  go test ./...
