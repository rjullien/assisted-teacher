#!/bin/sh
set -e

# Render models.json with the Bifrost API key (trimmed).
# pi's "$VAR" interpolation does NOT trim, and Infisical secrets often carry
# a trailing \n. The Go backend already does strings.TrimSpace on HERMES_API_KEY,
# but pi reads models.json directly — so we trim here.
if [ "$PI_ENABLED" = "true" ] && [ -n "$BIFROST_API_KEY" ]; then
  TRIMMED_KEY=$(printf '%s' "$BIFROST_API_KEY" | tr -d '[:space:]')
  sed "s|__BIFROST_API_KEY__|${TRIMMED_KEY}|g" /data/pi-config/models.json.tmpl > /data/pi-config/models.json
  echo "pi: models.json rendered (keyLen=${#TRIMMED_KEY})"
fi

exec assisted-teacher "$@"
