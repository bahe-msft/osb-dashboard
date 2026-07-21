# OpenSandbox Dashboard

A lightweight Go and HTMX dashboard for discovering and operating sandboxes in
an OpenSandbox Kubernetes cluster.

## Run locally

```bash
just dev /path/to/kubeconfig
```

The dashboard listens on `127.0.0.1:8080` by default. Override the address with
`HTTP_ADDR`.

### Subpath hosting

Use `--base-path` (or `OSB_DASHBOARD_BASE_PATH`) when the dashboard is exposed
beneath a URL prefix:

```bash
go run . \
  --kubeconfig /path/to/kubeconfig \
  --base-path /dashboard
```

The application will then serve its UI, assets, HTMX endpoints, browser-history
URLs, forms, and terminal WebSocket beneath `/dashboard`. Configure the reverse
proxy to forward that prefix unchanged; requests to `/dashboard` redirect to
`/dashboard/`.

## Container image

Pushes to `main`, version tags such as `v1.2.3`, and manual workflow runs publish
a multi-platform image to:

```text
ghcr.io/bahe-msft/osb-dashboard
```

The container runs as a non-root user. Mount a kubeconfig and provide an auth
token when exposing the dashboard outside the container:

```bash
docker run --rm -p 8080:8080 \
  -e HTTP_ADDR=0.0.0.0:8080 \
  -e OSB_DASHBOARD_AUTH_TOKEN='replace-with-a-strong-token' \
  -e OSB_DASHBOARD_BASE_PATH='/dashboard' \
  -v "$HOME/.kube/config:/config/kubeconfig:ro" \
  ghcr.io/bahe-msft/osb-dashboard:latest \
  --kubeconfig /config/kubeconfig
```

With the example above, open `http://localhost:8080/dashboard/`.

Published images support `linux/amd64` and `linux/arm64`. The workflow also
publishes branch, semantic-version, and commit-SHA tags and attaches provenance
and an SBOM. After the first publish, set the package visibility to **Public** in
GitHub Packages if anonymous pulls are required.

## Authentication

Loopback development can run without authentication. A non-loopback
`HTTP_ADDR` requires a dashboard token:

```bash
HTTP_ADDR=0.0.0.0:8080 \
OSB_DASHBOARD_AUTH_TOKEN='replace-with-a-strong-token' \
go run . --kubeconfig /path/to/kubeconfig
```

Clients may authenticate with either:

- HTTP Basic authentication, using the token as the password; or
- `Authorization: Bearer <token>`.

Non-browser clients that call mutation endpoints must also send
`X-OSB-CSRF: 1`. Browser mutations and terminal WebSockets are restricted to the
same origin.

For production, terminate TLS at a trusted reverse proxy and keep the dashboard
on a private network.

## Tests

```bash
just test
just e2e /path/to/isolated-cluster.kubeconfig
```

The live E2E suite creates and deletes a sandbox. See [e2e/README.md](e2e/README.md)
for categories, configuration, traces, and video recordings.
