# OpenSandbox Dashboard

A lightweight Go and HTMX dashboard for discovering and operating sandboxes in
an OpenSandbox Kubernetes cluster.

## Run locally

```bash
just dev /path/to/kubeconfig
```

The dashboard listens on `127.0.0.1:8080` by default. Override the address with
`HTTP_ADDR`.

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
