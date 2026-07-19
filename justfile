# Run the development server with a kubeconfig for an OpenSandbox cluster.
dev kubeconfig:
  go run . --kubeconfig "{{kubeconfig}}"

# Build the dashboard binary.
build:
  go build -o osb-dashboard .

# Run all tests.
test:
  go test ./...
