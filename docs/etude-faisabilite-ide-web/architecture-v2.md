# Architecture v2 — Client ACP web pour authoring de cours

## Le use case

> Un prof d'anglais construit ses cours avec l'IA.
> Elle veut tout suivre, tout contrôler, tout valider.
> VS Code c'est pas adapté à un prof d'anglais.
> On veut une **vraie interface ACP** — le standard ouvert pour parler aux agents.

---

## Contraintes

| Contrainte | Décision |
|---|---|
| Protocole | **ACP (Agent Client Protocol)** — le "LSP pour agents" |
| Multi-user | Archi day 1, mono-user MVP0 |
| Stockage | Local MVP0, GitHub v1 |
| Format source | Markdown |
| Format output | PDF (Typst), DOCX (Pandoc) |
| LLM backend | Bifrost (OpenAI-compatible, déjà déployé) |
| Agent backend | N'importe quel agent ACP : OpenCode, OpenClaw, Kiro CLI, Hermes… |
| Self-hostable | Docker, k3s |
| Pas VS Code | Interface simple, pour non-dev |

---

## Ce qui existe déjà (découverte clé)

### Clients ACP web open source

| Projet | Description | Pertinence |
|---|---|---|
| **[acp-ui](https://github.com/formulahendry/acp-ui)** | Client ACP universel (web + desktop + mobile). Vue 3 + Tauri. Chat, sessions, permissions, modèles. Connecte à tout agent ACP via WebSocket. | ⭐⭐⭐ |
| **[Vibekit](https://github.com/cplieger/vibekit)** | Workspace web complet pour Kiro CLI via ACP. File browser + éditeur + terminal + chat + git. Docker multi-arch. SSE sync multi-device. | ⭐⭐⭐ |
| **[Casper](https://agentclientprotocol.com/get-started/clients)** | Web client pour kiro-cli via ACP. Chat UI avec streaming et tool-call rendering. | ⭐⭐ |
| **[Kiro2Chat](https://github.com/aleck31/Kiro2Chat)** | Bridge kiro-cli vers Telegram/Lark/Discord/Web via ACP. | ⭐ |

### Agents ACP existants (côté serveur)

| Agent | Commande ACP | Notes |
|---|---|---|
| **OpenCode** | `npx opencode-ai@latest acp` | 150k★, multi-provider |
| **OpenClaw** | `npx openclaw acp` | Ton propre agent |
| **Kiro CLI** | `kiro-cli acp` | AWS-backed |
| **Claude Code** | `npx @agentclientprotocol/claude-agent-acp@latest` | Anthropic officiel |
| **Gemini CLI** | `npx @google/gemini-cli@latest --experimental-acp` | Google |
| **Hermes Agent** | `hermes acp` | NousResearch |
| **Codex CLI** | `npx @zed-industries/codex-acp@latest` | OpenAI/Zed |

### SDK

| Lang | Package | Version |
|---|---|---|
| TypeScript | `@agentclientprotocol/sdk` | v1.0 (avril 2026) |
| Rust | `agent-client-protocol` | v1.0 |
| Python | `python-sdk` | stable |
| Kotlin | `acp-kotlin` | JVM |

---

## Stratégie : fork Vibekit + adapter l'UX pour un prof

### Pourquoi Vibekit comme base

Vibekit donne **exactement** le layout 3 panneaux qu'on veut :
- 📁 File browser (gauche)
- 📝 Éditeur avec syntax highlighting (centre)
- 💬 Chat ACP avec streaming (droite)
- 🖥️ Terminal (bas, optionnel)
- 🔀 Git intégré

C'est un **vrai client ACP** qui :
- Lance n'importe quel agent ACP en subprocess
- Gère sessions, permissions, modes (ask/code/autonomous)
- Stream les réponses en SSE multi-device
- Tourne en Docker (multi-arch amd64 + arm64)
- Est open source (MIT)

### Ce qu'on adapte pour le prof

| Vibekit (dev) | Notre version (prof) |
|---|---|
| Éditeur code (syntax highlight) | Éditeur **Markdown WYSIWYG** (Milkdown) |
| Terminal visible | Terminal caché (avancé seulement) |
| Git UI technique | "Sauvegarder" / "Historique" simplifié |
| Modes agent: code/spec/plan | Modes: "Générer exercice" / "Corriger" / "Reformuler" |
| UI sombre dev | UI claire, friendly |
| Pas d'export | **Export Typst → PDF** et **Pandoc → DOCX** |

---

## Architecture cible

```
┌─────────────────────────────────────────────────────────────┐
│                     Navigateur (prof)                         │
├──────────────┬─────────────────────────┬────────────────────┤
│  📁 Mes cours │  📝 Milkdown (Md WYSIWYG) │  💬 Chat Agent    │
│              │                         │                    │
│  B1/         │  # Unit 5               │  🤖 Streaming...   │
│    unit5.md  │  ## Past Perfect        │                    │
│    unit6.md  │  Complete the gaps...   │  [Insérer]         │
│  Vocab/      │                         │  [Remplacer]       │
│              │  [📄 PDF] [📄 DOCX]     │  [Refuser]         │
└──────────────┴─────────────────────────┴────────────────────┘
        │                  │                       │
        └──────────────────┼───────────────────────┘
                           │
              ACP (JSON-RPC over WebSocket)
                           │
┌──────────────────────────┴──────────────────────────────────┐
│                    Serveur (Docker)                           │
│                                                              │
│  ┌─────────────────────┐    ┌────────────────────────────┐  │
│  │   App Server (Go)   │    │   Agent ACP (subprocess)   │  │
│  │                     │    │                            │  │
│  │  • File API         │    │  openclaw acp              │  │
│  │  • Auth (JWT)       │    │  (ou opencode-ai acp)      │  │
│  │  • Export service   │    │  (ou kiro-cli acp)         │  │
│  │  • ACP bridge WS    │◄──►│  (ou hermes acp)           │  │
│  │  • SSE hub          │    │                            │  │
│  │                     │    │  Parle à Bifrost /v1       │  │
│  └─────────┬───────────┘    └────────────────────────────┘  │
│            │                                                 │
│  ┌─────────┴───────────┐    ┌────────────────────────────┐  │
│  │  Store              │    │  Export                    │  │
│  │  • Local fs (MVP0)  │    │  • typst (binaire Rust)   │  │
│  │  • GitHub API (v1)  │    │  • pandoc (md→docx)       │  │
│  └─────────────────────┘    └────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
                           │
                    Bifrost /v1 (LLM gateway)
                           │
              ┌────────────┼────────────┐
              │            │            │
          Claude       DeepSeek      Gemini
         (Bedrock)   (OpenCode Go) 
```

---

## Le protocole ACP en détail

### Pourquoi c'est le bon choix

ACP = **Agent Client Protocol**, créé par Zed (l'éditeur), co-signé par JetBrains. C'est le standard qui :

1. **Découple l'UI de l'agent** — on peut changer d'agent sans toucher au frontend
2. **Gère les permissions nativement** — l'agent demande "je veux écrire dans unit5.md", le prof accepte ou refuse
3. **Supporte le streaming** — réponses progressives, pas de timeout
4. **A des SDKs officiels** — TypeScript v1.0 pour le client web
5. **Est adopté par tous** — Claude Code, Gemini, OpenCode, Copilot, Hermes, Kiro...

### Flow ACP typique

```
Prof tape: "Génère 3 exercices gap-fill past perfect B1"

Client (notre UI web)                    Agent (OpenClaw/OpenCode via ACP)
        │                                           │
        │── initialize ────────────────────────────→│
        │←─ {capabilities, protocolVersion} ────────│
        │                                           │
        │── session/create ────────────────────────→│
        │←─ {sessionId} ───────────────────────────│
        │                                           │
        │── prompt/start ──────────────────────────→│
        │   {message: "Génère 3 exercices..."}      │
        │                                           │
        │←─ notification/progress (streaming) ─────│  ← le prof voit apparaître
        │←─ notification/progress (streaming) ─────│    le texte mot par mot
        │←─ notification/progress (streaming) ─────│
        │                                           │
        │←─ client/requestPermission ──────────────│  ← "Je veux écrire dans
        │   {action: "write", path: "B1/unit5.md"} │     B1/unit5.md"
        │                                           │
        │  [Prof clique "Accepter"]                 │
        │── client/requestPermission result ───────→│
        │   {granted: true}                         │
        │                                           │
        │←─ prompt/end ────────────────────────────│
        │   {result: "done", filesModified: [...]}  │
        │                                           │
        │  [Milkdown recharge le fichier modifié]   │
```

### Le bridge WebSocket

Le navigateur ne peut pas lancer un subprocess (l'agent ACP tourne en stdio par défaut). Solution standard :

```
Navigateur ──WebSocket──→ Serveur Go ──stdio──→ Agent ACP process
```

Le serveur fait office de **bridge** : il reçoit les messages JSON-RPC du client web via WebSocket et les forward à l'agent ACP sur stdin/stdout. C'est exactement ce que fait `@rebornix/stdio-to-ws` ou ce que Vibekit implémente nativement.

---

## Briques open source

### Frontend

| Composant | Brique | Licence |
|---|---|---|
| Framework | **React + Vite** (ou Vue comme acp-ui) | MIT |
| Éditeur Markdown WYSIWYG | **[Milkdown](https://milkdown.dev/)** | MIT |
| File tree | **[react-arborist](https://github.com/brimdata/react-arborist)** | MIT |
| Chat + streaming | **@agentclientprotocol/sdk** (TypeScript) | Apache 2.0 |
| Split panes | **[allotment](https://github.com/johnwalley/allotment)** | MIT |
| Markdown rendering (chat) | **react-markdown** ou **marked** | MIT |

### Backend (Go, binaire unique)

| Composant | Rôle |
|---|---|
| HTTP server | Sert le frontend SPA + API REST (files, auth, export) |
| WebSocket bridge | Reçoit JSON-RPC ACP du navigateur, forward vers l'agent subprocess |
| Agent subprocess | Lance `openclaw acp` (ou tout autre agent ACP) en stdin/stdout |
| File store | CRUD local (MVP0), GitHub API (v1) |
| Export | Shell out vers `typst compile` et `pandoc` |
| Auth | JWT, multi-user ready |
| SSE hub | Notifications temps réel (file changes, etc.) |

### Export

| Pipeline | Outils |
|---|---|
| **Markdown → PDF** | `typst compile` avec template personnalisé (via [cmarker](https://typst.app/universe/package/cmarker/) pour importer le Markdown dans Typst) |
| **Markdown → PDF (alt)** | `pandoc input.md --pdf-engine=typst -o output.pdf` |
| **Markdown → DOCX** | `pandoc input.md --reference-doc=template.docx -o output.docx` |
| **API HTTP** | [typst-http-api](https://github.com/slashformotion/typst-http-api) si on veut un microservice séparé |

### Infra

| Service | Rôle |
|---|---|
| **Bifrost** (déjà déployé) | LLM gateway → Claude, DeepSeek, Gemini |
| **typst** (binaire Rust) | Compilation PDF |
| **pandoc** (dans le container) | Conversion DOCX |

---

## Multi-user : design day 1, implémentation progressive

### Modèle

```
/data/
  users.db              (SQLite — users, sessions, tokens)
  workspaces/
    ws-{uuid}/
      meta.json         {id, name, owner_id, created_at, members[]}
      files/
        B1/
          unit5.md
        Vocab/
          animals.md
      exports/
        unit5.pdf
      .agent/           (config agent spécifique au workspace)
```

### Isolation

- Chaque workspace est isolé (un agent ACP par workspace actif)
- L'agent ACP reçoit le `workDir` correspondant au workspace
- Les permissions ACP empêchent l'agent de sortir du workspace

### Auth progression

| Phase | Auth |
|---|---|
| MVP0 | Token statique dans .env (un seul user) |
| v1 | JWT + login/password (SQLite) |
| v2 | OAuth Google (comptes école) |

---

## Choix d'agent backend

L'avantage d'ACP : **on s'en fout de l'agent**. Le frontend est identique. On peut switcher :

| Agent | Avantage | Inconvénient |
|---|---|---|
| **OpenClaw** | Le tien, tu contrôles tout, déjà branché sur Bifrost | Faut le maintenir |
| **OpenCode** | 150k★, très mature, multi-provider | Pas de custom system prompt facile |
| **Kiro CLI** | Puissant, specs/steering natifs | Dépend AWS |
| **Hermes** | Mémoire, skills, autonomie | Plus lourd à setup |
| **Claude Code ACP** | Directement Anthropic | Coût API Anthropic |

**Recommandation MVP0** : OpenClaw (tu contrôles le system prompt pédagogique et il tape déjà dans Bifrost).

---

## Plan d'exécution MVP0

### Phase 1 — Skeleton (3-4 jours)

- [ ] Init repo (nom à choisir)
- [ ] Backend Go : HTTP server + WebSocket ACP bridge + file API
- [ ] Lancer un agent ACP en subprocess (OpenClaw ou OpenCode)
- [ ] Frontend : layout 3 panneaux + file tree + Milkdown vide
- [ ] Connexion : file tree → ouvre fichier dans Milkdown

### Phase 2 — Chat ACP (3-4 jours)

- [ ] Intégrer `@agentclientprotocol/sdk` côté frontend
- [ ] Implémenter le flow : initialize → session/create → prompt/start → streaming
- [ ] Afficher les réponses en streaming dans le panneau chat
- [ ] Implémenter les permission prompts (accepter/refuser les modifications)
- [ ] Bouton "Insérer" qui accepte la modification et recharge le fichier

### Phase 3 — Export Typst/Pandoc (2 jours)

- [ ] Installer typst + pandoc dans le container
- [ ] Template Typst pour cours d'anglais
- [ ] Routes `POST /export/pdf` et `POST /export/docx`
- [ ] Boutons dans l'UI

### Phase 4 — Polish + Deploy (2-3 jours)

- [ ] Auth basique (token .env)
- [ ] Dockerfile multi-stage (Go + frontend + typst + pandoc)
- [ ] docker-compose.yml
- [ ] Deploy k3s (à côté de Bifrost)
- [ ] README + doc utilisateur

### Total estimé : ~12-14 jours

---

## Évolutions

| Version | Feature |
|---|---|
| v0.1 | MVP0 mono-user, local files, export PDF/DOCX |
| v0.2 | Multi-user (JWT, workspaces isolés) |
| v0.3 | GitHub sync (commit/push les fichiers) |
| v1.0 | Templates Typst personnalisables, modes agent custom |
| v1.1 | Collab temps réel (Yjs + Milkdown) |
| v2.0 | OAuth Google, marketplace de prompts pédagogiques |

---

## Différences vs Vibekit / acp-ui

| | Vibekit | acp-ui | Notre projet |
|---|---|---|---|
| Cible | Développeurs | Développeurs | **Profs / non-devs** |
| Éditeur | Code (syntax) | Chat seulement | **Markdown WYSIWYG** |
| Export | Non | Non | **PDF (Typst) + DOCX** |
| File browser | Oui | Non | Oui (simplifié) |
| Terminal | Oui (visible) | Non | Caché |
| Agent | Kiro CLI | Tout ACP | **Tout ACP** (OpenClaw par défaut) |
| System prompt | Générique code | N/A | **Pédagogique** |
| Multi-user | Non | Non | **Oui (archi day 1)** |

---

## Décision Build vs Fork

| Option | Effort | Contrôle | UX non-dev |
|---|---|---|---|
| **Fork Vibekit** et adapter | ~8 jours (Go + Vue existants) | Moyen | Moyen (faut refaire beaucoup d'UI) |
| **Fork acp-ui** web + ajouter file/editor | ~10 jours (Vue + Tauri web build) | Moyen | Bon (UI déjà propre) |
| **Build from scratch** avec le SDK ACP TS | ~14 jours | Total | Total |

**Recommandation** : **Build from scratch avec le SDK ACP TypeScript**. Pourquoi :
- Le SDK fait le gros du travail (JSON-RPC, sessions, streaming)
- Milkdown est incompatible avec les éditeurs existants des forks (faut tout refaire anyway)
- On contrôle 100% de l'UX "non-dev"
- Le backend Go est simple (bridge WS + file API + export)
- En 14 jours c'est fait proprement

---

## Liens

- **ACP Spec** : [agentclientprotocol.com](https://agentclientprotocol.com/)
- **ACP TypeScript SDK** : [github.com/agentclientprotocol/typescript-sdk](https://github.com/agentclientprotocol/typescript-sdk)
- **acp-ui** (client universel) : [github.com/formulahendry/acp-ui](https://github.com/formulahendry/acp-ui)
- **Vibekit** (workspace web ACP) : [github.com/cplieger/vibekit](https://github.com/cplieger/vibekit)
- **stdio-to-ws** (bridge ACP) : [@rebornix/stdio-to-ws](https://www.npmjs.com/package/@rebornix/stdio-to-ws)
- **Milkdown** : [milkdown.dev](https://milkdown.dev/)
- **Typst** : [typst.app](https://typst.app/)
- **typst-http-api** : [github.com/slashformotion/typst-http-api](https://github.com/slashformotion/typst-http-api)
- **cmarker** (Md→Typst) : [typst.app/universe/package/cmarker](https://typst.app/universe/package/cmarker/)
- **Pandoc** : [pandoc.org](https://pandoc.org/)
- **Bifrost** : [github.com/maximhq/bifrost](https://github.com/maximhq/bifrost)

---

*Architecture v2 — 22 août 2026*
*ACP-native, Typst pour le rendu, multi-agent compatible.*
