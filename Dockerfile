# --- Stage 1: Build frontend ---
FROM node:22-alpine AS frontend-build
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install
COPY frontend/ ./
RUN npm run build
# Output: /app/frontend/dist → goes to backend/static/

# --- Stage 2: Build backend ---
FROM golang:1.23-alpine AS backend-build
RUN apk add --no-cache git
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Copy frontend build output into static/ for embedding
COPY --from=frontend-build /app/backend/static ./static/
RUN CGO_ENABLED=0 GOOS=linux go build -o /cours-ia .

# --- Stage 3: Runtime ---
FROM alpine:3.20
RUN apk add --no-cache \
    ca-certificates \
    pandoc \
    nodejs \
    npm \
    && rm -rf /var/cache/apk/*

# Install Typst
RUN wget -qO- https://github.com/typst/typst/releases/latest/download/typst-x86_64-unknown-linux-musl.tar.xz \
    | tar xJ --strip-components=1 -C /usr/local/bin/ typst-x86_64-unknown-linux-musl/typst

# Install ACP agents (optional — can be configured at runtime)
# RUN npm install -g opencode-ai openclaw

COPY --from=backend-build /cours-ia /usr/local/bin/cours-ia

# Create workspace and config directories
RUN mkdir -p /workspace /config

# Default env
ENV PORT=9847
ENV WORKSPACE_DIR=/workspace
ENV ACP_AGENT_CMD=""

EXPOSE 9847

ENTRYPOINT ["cours-ia"]
