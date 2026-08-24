#!/bin/sh
set -e

# Pi reads its configuration from $HOME/.pi/agent/ and nowhere else. There is no
# PI_MODELS_JSON environment variable — an earlier version of this file invented
# one, so pi never saw the provider config and exited 1.
#
# Also note pi caches its model catalog in $HOME/.pi/agent/models-store.json, so
# this directory has to be writable, not just readable.

PI_HOME="${HOME:-/root}"
PI_AGENT_DIR="$PI_HOME/.pi/agent"

if [ "$PI_ENABLED" = "true" ]; then
  mkdir -p "$PI_AGENT_DIR"

  # Bifrost speaks the OpenAI Chat Completions API and, from inside the cluster,
  # accepts requests without a key. Same endpoint tripkit-backend uses.
  LLM_URL=$(printf '%s' "${BIFROST_URL:-http://bifrost.openclaw.svc.cluster.local:8080/v1}" | tr -d '[:space:]')
  LLM_MODEL_ID=$(printf '%s' "${PI_MODEL_ID:-opencode-go/deepseek-v4-flash}" | tr -d '[:space:]')

  # A key is optional. When one is provided, trim it — Infisical secrets
  # routinely carry a trailing newline, which would end up inside the
  # Authorization header. When absent, pi still needs a non-empty apiKey for the
  # model to be considered available, so use a placeholder and do NOT send an
  # Authorization header.
  RAW_KEY="${BIFROST_API_KEY:-}"
  TRIMMED_KEY=$(printf '%s' "$RAW_KEY" | tr -d '[:space:]')
  if [ -n "$TRIMMED_KEY" ]; then
    LLM_KEY="$TRIMMED_KEY"
    AUTH_HEADER="true"
    echo "pi: models.json using a Bifrost API key (len=${#TRIMMED_KEY})"
  else
    LLM_KEY="bifrost-no-auth-needed"
    AUTH_HEADER="false"
    echo "pi: models.json without Authorization header (Bifrost is keyless in-cluster)"
  fi

  sed -e "s|__LLM_API_URL__|${LLM_URL}|g" \
      -e "s|__LLM_API_KEY__|${LLM_KEY}|g" \
      -e "s|__LLM_AUTH_HEADER__|${AUTH_HEADER}|g" \
      -e "s|__LLM_MODEL_ID__|${LLM_MODEL_ID}|g" \
      /data/pi-config/models.json.tmpl > "$PI_AGENT_DIR/models.json"

  cp /data/pi-config/settings.json "$PI_AGENT_DIR/settings.json"

  echo "pi: config written to $PI_AGENT_DIR (url=$LLM_URL, model=$LLM_MODEL_ID)"

  # Fail loudly at boot rather than on the teacher's first prompt.
  if command -v pi >/dev/null 2>&1; then
    echo "pi: version $(PI_OFFLINE=1 pi --version 2>&1 | head -1)"
  else
    echo "pi: WARNING binary not found in PATH — mode Pi will fail"
  fi
fi

exec assisted-teacher "$@"
