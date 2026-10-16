# syntax=docker/dockerfile:1
FROM node:22-bookworm-slim@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392 AS ui
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=ui /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -o /flyway-manager ./cmd/flyway-manager

# Acquisition is build-time only. Preserve the approved full vendor tree/notices.
FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
WORKDIR /download
RUN curl --fail --location --proto '=https' --tlsv1.2 --output flyway.tar.gz https://download.red-gate.com/maven/release/com/redgate/flyway/flyway-commandline/13.10.3/flyway-commandline-13.10.3-linux-x64.tar.gz \
    && echo 'f0451f0ef9d1c558d5e1fd5a35ad65f039bebecd4eb2007a87022da21fc2dccf  flyway.tar.gz' | sha256sum -c - \
    && mkdir /opt/flyway && tar -xzf flyway.tar.gz --strip-components=1 -C /opt/flyway
COPY deploy/runtime/flyway-13.10.3.sha256 /opt/flyway/flyway-manifest.sha256
WORKDIR /opt/flyway
RUN echo 'a359f1f0e0a08bd7f5fe3272cc726c3866c0000028fee32a1df56a61f85ecd2c  flyway-manifest.sha256' | sha256sum -c - \
    && sha256sum --quiet -c flyway-manifest.sha256 \
    && find . -type d -exec chmod 0555 {} + \
    && find . -type f -perm /111 -exec chmod 0555 {} + \
    && find . -type f ! -perm /111 -exec chmod 0444 {} +

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Flyway Manager" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION \
      org.opencontainers.image.source="https://github.com/longhn0710/flyway-management"
# The vendor's bundled RGCompare version probe requires ICU even for CLI -v.
RUN apt-get update && apt-get install -y --no-install-recommends libicu72 && rm -rf /var/lib/apt/lists/*
COPY --from=runtime /etc/ssl/certs/ /etc/ssl/certs/
COPY --from=runtime /opt/flyway /opt/flyway
COPY --from=server /flyway-manager /usr/local/bin/flyway-manager
RUN mkdir -p /data && chown 10001:10001 /data
ENV FLYWAY_MANAGER_LISTEN_ADDRESS=0.0.0.0:8080 FLYWAY_MANAGER_DATA_DIR=/data/manager HOME=/tmp
USER 10001:10001
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/flyway-manager"]
