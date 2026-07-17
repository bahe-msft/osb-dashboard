# Run the development server.
dev:
  go run .

# Build the dashboard binary.
build:
  go build -o osb-dashboard .

# Run all tests.
test:
  go test ./...
