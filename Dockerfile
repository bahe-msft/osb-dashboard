# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN ./scripts/fetch-ghostty-web.sh \
    && ./scripts/fetch-ui-assets.sh \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
       go build -trimpath -ldflags='-s -w' -o /out/osb-dashboard .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/osb-dashboard /usr/local/bin/osb-dashboard

EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/osb-dashboard"]
