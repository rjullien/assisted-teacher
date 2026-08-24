#!/usr/bin/env bash
set -euo pipefail

# Capture BRUTE des frames SSE que Hermes (Lya) renvoie sur
# POST $HERMES_URL/v1/chat/completions.
#
# Pourquoi ce script existe : v1.9.0, v1.10.0 et v1.10.1 ont tenté de recopier
# les écritures de Lya en supposant la forme de ses frames `hermes.tool.progress`
# (clés name/path/content/status, statut "done"). Personne n'a jamais vu une
# vraie frame. Ce script fournit la preuve — le nom d'événement et les clés
# réellement envoyés — sans passer par l'application.
#
# Le pendant côté application est HERMES_TRACE_EVENTS=true (voir README) : à
# utiliser quand c'est le comportement du backend qu'on veut observer. Ce script
# sert quand on veut la frame brute, sans backend entre les deux.
#
# Prérequis : accès réseau à $HERMES_URL (Tailscale / intra-cluster) + la clé
# gateway ($HERMES_API_KEY == API_SERVER_KEY de Lya, cf. /opt/data/.env du PVC).
#
# Usage :
#   HERMES_URL=http://hermes-lya.openclaw.svc.cluster.local:8642 \
#   HERMES_API_KEY=… \
#   ./scripts/capture-hermes-frames.sh [prompt] [fichier-de-sortie]
#
# Depuis un pod du cluster (le backend a déjà les deux variables) :
#   kubectl exec -n assisted-teacher deploy/assisted-teacher-backend -- \
#     sh -c 'curl -sS -N -X POST "$HERMES_URL/v1/chat/completions" \
#       -H "Authorization: Bearer $HERMES_API_KEY" \
#       -H "Content-Type: application/json" -H "Accept: text/event-stream" \
#       -d "{\"messages\":[{\"role\":\"user\",\"content\":\"Ajoute une ligne à la fin de Test_folders/test_nvx_cours.md\"}],\"provider\":\"custom\",\"stream\":true}"'
#
# La clé n'est JAMAIS affichée : seule son empreinte (8 hex de SHA-256) l'est,
# la même que la ligne « hermes: bridge configured … keyFp=… » des logs backend.

# Le prompt par défaut est celui de l'incident de production, pour capturer la
# frame de l'échange qui a échoué trois fois. Défini dans une variable à part :
# une apostrophe dans un ${1:-…} est interprétée par bash comme une ouverture de
# quote, même entre guillemets.
DEFAULT_PROMPT="Ajoute la ligne « Ligne ajoutée pour tester l'écriture dans le fichier. » à la fin de Test_folders/test_nvx_cours.md, en conservant le contenu existant."
PROMPT="${1:-$DEFAULT_PROMPT}"
OUT="${2:-hermes-frames-$(date +%Y%m%d-%H%M%S).sse}"

: "${HERMES_URL:?HERMES_URL manquant (ex. http://hermes-lya.openclaw.svc.cluster.local:8642)}"
: "${HERMES_API_KEY:?HERMES_API_KEY manquant (= API_SERVER_KEY de Lya)}"

URL="${HERMES_URL%/}/v1/chat/completions"
KEY_FP="$(printf '%s' "$HERMES_API_KEY" | tr -d '\n' | sha256sum | cut -c1-8)"

echo "→ POST $URL (keyFp=$KEY_FP, keyLen=${#HERMES_API_KEY})"
echo "→ frames brutes écrites dans $OUT"
echo

# --no-buffer : on veut les frames au fil de l'eau, pas à la fin.
# Le corps est construit avec un heredoc pour ne pas avoir à échapper le prompt.
payload_file="$(mktemp)"
trap 'rm -f "$payload_file"' EXIT
PROMPT="$PROMPT" python3 - > "$payload_file" <<'PY'
import json, os
print(json.dumps({
    "messages": [{"role": "user", "content": os.environ["PROMPT"]}],
    "provider": "custom",
    "stream": True,
}))
PY

curl -sS -N --no-buffer -X POST "$URL" \
  -H "Authorization: Bearer $HERMES_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -H "User-Agent: assisted-teacher-capture" \
  --data-binary "@$payload_file" | tee "$OUT"

echo
# Un corps d'erreur HTTP n'est pas un flux SSE : le dire, sinon le résumé
# ci-dessous conclurait « aucune frame d'outil » sur une requête jamais traitée.
if ! grep -q '^data:' "$OUT"; then
  echo "── Réponse non-SSE : la requête n'a pas été traitée ───────────────────"
  head -c 500 "$OUT"
  echo
  echo "→ HTTP 401/403 : HERMES_API_KEY ≠ API_SERVER_KEY de Lya (voir /opt/data/.env du PVC, README)."
  echo "→ HTTP 503 « Auth provider … unreachable » : la gateway a refusé la clé en amont."
  exit 1
fi

echo "── Noms d'événements SSE vus ──────────────────────────────────────────"
if grep -q '^event:' "$OUT"; then
  grep '^event:' "$OUT" | sort | uniq -c
else
  echo "AUCUN. Le flux ne contenait que des frames 'data:' (chat.completion.chunk)."
  echo "→ Lya n'a signalé aucune activité d'outil : le problème est en amont du"
  echo "  backend, et rien de ce que fait handleToolFileWrite ne peut y changer"
  echo "  quoi que ce soit."
fi
echo
echo "── Clés de la première frame d'outil ─────────────────────────────────"
first_tool_data="$(grep -A1 '^event:' "$OUT" | grep '^data:' | head -1 | sed 's/^data: *//')" || true
if [ -n "${first_tool_data:-}" ]; then
  printf '%s' "$first_tool_data" | python3 -c 'import json,sys; print(sorted(json.load(sys.stdin).keys()))' 2>/dev/null \
    || echo "(payload non-JSON, voir $OUT)"
else
  echo "(aucune frame d'outil)"
fi
