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

### Auth vers Lya (Hermès)

Le backend n'utilise plus de subprocess ACP : il appelle Lya en HTTP streaming (`POST /v1/chat/completions` + `Authorization: Bearer …`).

Si le chat affiche `Échec IA` avec un détail `hermes auth failed (HTTP 401)` / `Invalid gateway API key`, ce n'est **pas** Authelia : c'est la clé gateway Hermes. Aligner `HERMES_API_KEY` (secret assisted-teacher) sur `API_SERVER_KEY` du pod `hermes-lya`.

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
