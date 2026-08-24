# Cours IA — Outil d'authoring assisté par IA

Client web ACP (Agent Client Protocol) pour créer des cours avec l'aide de l'intelligence artificielle.

Conçu pour les **enseignants** : interface simple avec arbre de fichiers, éditeur Markdown WYSIWYG et chat IA.

## Layout

```
┌──────────────┬───────────────────────────┬──────────────────────┐
│  📁 Mes cours │      📝 Éditeur Markdown    │     💬 Assistant IA  │
│              │      (WYSIWYG, Milkdown)  │     (ACP streaming)  │
│  B1/         │                           │                      │
│    unit5.md  │  # Unit 5 — Past Perfect  │  🤖 "Voici 3 exerc." │
│    unit6.md  │                           │                      │
│  Vocab/      │  ## Exercice 1            │  [Insérer] [Copier]  │
│              │                           │                      │
└──────────────┴───────────────────────────┴──────────────────────┘
```

## Architecture

- **Frontend** : React + Vite + Milkdown (Markdown WYSIWYG) + Allotment (split panes)
- **Backend** : Go (binaire unique) — file API, WebSocket ACP bridge, export service
- **Agent AI** : N'importe quel agent ACP via subprocess (OpenCode, OpenClaw, Kiro CLI, Hermes, Claude Code, Gemini CLI, etc.)
- **Export** : Typst → PDF, Pandoc → DOCX
- **Protocole** : [Agent Client Protocol (ACP)](https://agentclientprotocol.com/) — JSON-RPC over WebSocket

## Quick start

```bash
# 1. Clone
cd cours-ia

# 2. Configurer
cp .env.example .env
# Éditer .env : choisir un agent ACP et mettre vos clés API

# 3. Lancer
docker compose up --build

# 4. Ouvrir
# → http://localhost:9847
```

## Développement local

### Backend

```bash
cd backend
go run . --workdir ../workspace --agent "opencode-ai acp"
```

### Frontend

```bash
cd frontend
npm install
npm run dev
# → http://localhost:5173 (proxy vers le backend sur :9847)
```

## Agents ACP supportés

| Agent | Commande | Prérequis |
|---|---|---|
| OpenCode | `opencode-ai acp` | `npm install -g opencode-ai` |
| OpenClaw | `openclaw acp` | `npm install -g openclaw` |
| Kiro CLI | `kiro-cli acp` | [kiro.dev/docs/cli](https://kiro.dev/docs/cli/) |
| Hermes | `hermes acp` | [hermes-agent.nousresearch.com](https://hermes-agent.nousresearch.com/) |
| Claude Code | `npx @agentclientprotocol/claude-agent-acp@latest` | ANTHROPIC_API_KEY |
| Gemini CLI | `npx @google/gemini-cli@latest --experimental-acp` | Google auth |
| Codex CLI | `npx @zed-industries/codex-acp@latest` | OPENAI_API_KEY |

## Export

| Format | Moteur | Template |
|---|---|---|
| PDF | [Typst](https://typst.app/) | `templates/cours.typ` (personnalisable) |
| DOCX | [Pandoc](https://pandoc.org/) | `templates/reference.docx` (optionnel) |

Les fichiers Markdown sont la source de vérité. Les exports sont générés à la demande.

## Variables d'environnement

| Variable | Défaut | Description |
|---|---|---|
| `PORT` | `9847` | Port HTTP |
| `WORKSPACE_DIR` | `./workspace` | Dossier des cours |
| `HERMES_URL` | `http://hermes-lya.openclaw.svc.cluster.local:8642` | URL API server Hermes (Lya) |
| `HERMES_API_KEY` | — | Bearer key = `API_SERVER_KEY` de Lya (Infisical). Un mismatch → HTTP 401 `Invalid gateway API key` |
| `PI_ENABLED` | `true` | Active le mode Pi. À `false` : pas de bouton Pi, pas de route `/ws/agent/pi` |
| `PI_CMD` | `pi` | Binaire pi (installé dans l'image) |
| `PI_PROVIDER` | `bifrost` | Clé de provider dans `~/.pi/agent/models.json` |
| `PI_MODEL` | `bifrost-default` | Champ `name` du modèle, **pas** son `id` |
| `PI_MODEL_ID` | `opencode-go/deepseek-v4-flash` | Identifiant envoyé à Bifrost |
| `BIFROST_URL` | `http://bifrost.openclaw.svc.cluster.local:8080/v1` | Endpoint LLM de pi |
| `BIFROST_API_KEY` | — | Optionnelle : Bifrost est sans auth depuis le cluster |

### Auth vers Lya (Hermès)

Le backend n'utilise plus de subprocess ACP : il appelle Lya en HTTP streaming (`POST /v1/chat/completions` + `Authorization: Bearer …`).

Si le chat affiche `Échec IA` avec un détail `hermes auth failed (HTTP 401)` / `Invalid gateway API key`, ce n'est **pas** Authelia : c'est la clé gateway Hermes.

**Piège prod (vérifié Tailscale/k3s) :** Hermes valide `API_SERVER_KEY` depuis le fichier PVC `/opt/data/.env`, **pas** depuis l'env K8s/Infisical injectée dans le pod. Les deux peuvent diverger après un rotate Infisical. Alignement :

1. Infisical `hermes-lya-secret.API_SERVER_KEY` == `assisted-teacher-secret.API_SERVER_KEY` (== `HERMES_API_KEY`)
2. **et** `/opt/data/.env` `API_SERVER_KEY` sur le PVC `hermes-lya-data` == la même valeur
3. puis `kubectl -n openclaw rollout restart deploy/hermes-lya`

Même pattern possible sur `hermes-leo` / TripKit.

### Les trois modes

| Mode | Écran | Agent | Fichiers |
|---|---|---|---|
| **Desk** | 3 panneaux | Hermes/Lya via `/ws/acp` | L'enseignante écrit ; « Insérer » pour reprendre une réponse |
| **Pi** | les mêmes 3 panneaux | pi via `/ws/agent/pi` | **pi lit et écrit les fichiers lui-même** |
| **Lya** | plein écran | Hermes/Lya via `/ws/acp` | aucun |

Le mode Pi n'apparaît que si le serveur l'annonce via `GET /api/agents`. Un mode mémorisé mais indisponible retombe sur Desk.

### Configuration de pi

`entrypoint.sh` écrit `models.json` et `settings.json` dans `$HOME/.pi/agent/` au démarrage — **le seul endroit où pi les lit**. Il n'existe pas de variable d'environnement pour pointer ailleurs.

Frontière fichiers : `--tools read,edit,write,grep,find,ls` est une allowlist stricte pour **tous** les outils. `bash` et `powershell` en sont volontairement absents — un agent qui rédige des cours n'a pas besoin d'un shell, et un shell est le seul outil qu'aucune frontière de chemin ne peut contenir. Doublé par `defaultTools` dans `settings.json`, `defaultProjectTrust: never` et `--no-approve`. Le test `TestPi_ToolAllowlistExcludesShells` échoue si un shell réapparaît dans l'argv.

### Diagnostiquer le mode Pi

```bash
kubectl logs -n assisted-teacher deploy/assisted-teacher-backend | grep -E "^pi|pi stderr|agent_usage"
```

- `pi: config written to …` + `pi: version …` au boot : la config est en place
- `pi: WARNING binary not found in PATH` : l'image n'a pas pi
- `pi stderr: …` : le message d'erreur de pi lui-même, également remonté dans l'interface
- `agent_usage agent=… mode=… status=… tools=…` : une ligne par requête. `mode` distingue Hermes-outil (`desk`) de Hermes-compagnon (`lya`)

## Roadmap

- [x] MVP0 : layout 3 panneaux, file API, chat ACP, export
- [ ] Multi-user (JWT, workspaces isolés)
- [ ] GitHub sync (push/pull les fichiers du prof)
- [ ] Templates Typst personnalisables
- [ ] Modes agent custom (Générer exercice / Corriger / Reformuler)
- [ ] Collab temps réel (Yjs + Milkdown)
- [ ] OAuth Google (comptes école)

## Licence

MIT
