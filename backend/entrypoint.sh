#!/bin/sh
set -e

# Render models.json for pi.
# Bifrost is accessible without an API key from inside the cluster.
# Default URL matches the tripkit-backend pattern: bifrost.openclaw.svc.cluster.local:8080/v1
LLM_URL="${BIFROST_URL:-http://bifrost.openclaw.svc.cluster.local:8080/v1}"

if [ -f /data/pi-config/models.json.tmpl ]; then
  TRIMMED_URL=$(printf '%s' "$LLM_URL" | tr -d '[:space:]')
  sed -e "s|__LLM_API_URL__|${TRIMMED_URL}|g" \
      /data/pi-config/models.json.tmpl > /data/pi-config/models.json
  echo "pi: models.json rendered (url=${TRIMMED_URL})"
fi

exec assisted-teacher "$@"
