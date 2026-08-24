# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o assisted-teacher .

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.22

RUN apk add --no-cache \
    ca-certificates \
    pandoc \
    nodejs \
    npm \
    && rm -rf /var/cache/apk/*

# Install Typst
RUN wget -qO- https://github.com/typst/typst/releases/latest/download/typst-x86_64-unknown-linux-musl.tar.xz \
    | tar xJ --strip-components=1 -C /usr/local/bin/ typst-x86_64-unknown-linux-musl/typst

# Install pi coding agent (pinned version, --ignore-scripts per official docs)
RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent@0.84.2 \
    && npm cache clean --force

COPY --from=build /app/assisted-teacher /usr/local/bin/assisted-teacher

# Embed official programme JSON files into the image.
# The backend reads these from PROGRAMMES_DIR (fallback: WORKSPACE_DIR/.programmes).
COPY --from=build /app/programmes /data/programmes

# Pi configuration: settings.json (trust=never, no bash) + models.json template
COPY pi-config/ /data/pi-config/

RUN mkdir -p /workspace

ENV PORT=9847
ENV WORKSPACE_DIR=/workspace
ENV PROGRAMMES_DIR=/data/programmes

# Pi configuration
ENV PI_ENABLED=true
ENV PI_CMD=pi
ENV PI_MODELS_JSON=/data/pi-config/models.json

EXPOSE 9847

# Entrypoint script renders models.json with the Bifrost key, then starts the backend
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

CMD ["/entrypoint.sh"]
