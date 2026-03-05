# --- Build stage ---
# Explicitly use docker.io to ensure Podman support
FROM docker.io/golang:1.25.0-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the binary
# CGO_ENABLED=0 ensures a static Go binary, reducing dependency issues
# -ldflags="-s -w" strips debug information to reduce size
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o gopdfsuit ./cmd/gopdfsuit


# --- Final stage ---
# Explicitly use docker.io
FROM docker.io/debian:12-slim

ARG VERSION=latest
LABEL version=$VERSION

ENV DEBIAN_FRONTEND=noninteractive

# 1. Install dependencies + dumb-init
# dumb-init is CRITICAL for Podman/Docker when running browsers.
# Chromium creates many child processes; without dumb-init (PID 1),
# zombie processes will pile up and eventually crash the container.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    dumb-init \
    chromium \
    libxss1 \
    libasound2 \
    libnspr4 \
    libnss3 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdbus-1-3 \
    libdrm2 \
    libgbm1 \
    libgtk-3-0 \
    libx11-xcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    fonts-liberation \
    ca-certificates \
    && echo "Cleaning up..." \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# 2. Create a non-root user
# Podman prefers rootless execution. Running Chromium as root is widely
# discouraged and often fails without unsafe flags.
RUN groupadd -r appuser && useradd -r -g appuser -G audio,video appuser \
    && mkdir -p /app \
    && chown -R appuser:appuser /app

WORKDIR /app

# 3. Copy files with ownership set to the non-root user
# Using --chown here prevents doubling the layer size (copying as root then chowning)
COPY --from=builder --chown=appuser:appuser /app/gopdfsuit .
COPY --from=builder --chown=appuser:appuser /app/docs ./docs
COPY --from=builder --chown=appuser:appuser /app/LICENSE .
COPY --from=builder --chown=appuser:appuser /app/README.md .
COPY --from=builder --chown=appuser:appuser /app/temp_multiplepage.json .

# 4. Switch to non-root user
USER appuser

EXPOSE 8080

# 5. Use dumb-init as the entrypoint
# This ensures signals (like SIGTERM) are passed correctly to your app
ENTRYPOINT ["/usr/bin/dumb-init", "--"]

CMD ["./gopdfsuit"]
