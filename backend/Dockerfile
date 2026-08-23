# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o assisted-teacher .

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache \
    ca-certificates \
    pandoc \
    && rm -rf /var/cache/apk/*

# Install Typst
RUN wget -qO- https://github.com/typst/typst/releases/latest/download/typst-x86_64-unknown-linux-musl.tar.xz \
    | tar xJ --strip-components=1 -C /usr/local/bin/ typst-x86_64-unknown-linux-musl/typst

COPY --from=build /app/assisted-teacher /usr/local/bin/assisted-teacher

# Embed official programme JSON files into the image.
# The backend reads these from PROGRAMMES_DIR (fallback: WORKSPACE_DIR/.programmes).
COPY --from=build /app/programmes /data/programmes

RUN mkdir -p /workspace

ENV PORT=9847
ENV WORKSPACE_DIR=/workspace
ENV PROGRAMMES_DIR=/data/programmes

EXPOSE 9847

CMD ["assisted-teacher"]
