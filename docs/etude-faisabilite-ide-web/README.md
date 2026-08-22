# Étude de faisabilité — IDE Web self-hosted avec AI Chat

**Objectif** : Un environnement navigateur avec 3 panneaux :
- 🗂️ **Gauche** : arbre de fichiers
- 📝 **Centre** : éditeur de code
- 💬 **Droite** : chat AI (harness OpenCode, Hermès, ou autre)

**Contraintes** : self-hostable, 100% open source, accessible via navigateur.

---

## 1. Résumé exécutif

| Approche | Faisabilité | Effort | Recommandation |
|----------|:-----------:|:------:|:--------------:|
| **A — OpenCode Web + code-server (combo Docker)** | ✅✅✅ | Faible | ⭐ **Recommandé** |
| **B — wede + OpenCode sidecar** | ✅✅ | Moyen | Intéressant si collab |
| **C — Eclipse Theia + extension AI** | ✅✅ | Élevé | Over-engineered |
| **D — Custom (Monaco + xterm.js + chat)** | ✅ | Très élevé | Dernier recours |
| **E — Hermes Studio / Workspace** | ✅✅ | Moyen | Si déjà sur Hermès |

---

## 2. Approche A — OpenCode Web + code-server (⭐ RECOMMANDÉ)

### Principe

Combiner dans un seul container Docker :
- **[code-server](https://github.com/coder/code-server)** (70k+ ★) — VS Code complet dans le navigateur (arbre de fichiers + éditeur)
- **[OpenCode Web](https://opencode.ai/docs/web/)** (150k+ ★) — agent AI coding avec interface web native (chat + file tree + diff viewer)

Les deux partagent le même filesystem `/repos`.

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Navigateur                             │
├──────────────────┬──────────────────┬───────────────────┤
│  File Tree       │    Éditeur       │    AI Chat        │
│  (code-server)   │  (code-server)   │  (OpenCode Web)   │
└──────────────────┴──────────────────┴───────────────────┘
         │                    │                    │
         └────────────────────┴────────────────────┘
                              │
                    ┌─────────┴─────────┐
                    │   Docker / VPS    │
                    │                   │
                    │  code-server :8080│
                    │  opencode web:4096│
                    │  /repos (shared)  │
                    └───────────────────┘
```

### Ce qui existe déjà

Le projet **[joostme/opencode-docker](https://github.com/joostme/opencode-docker)** fait exactement ça :
- Container unique avec OpenCode + code-server + Playwright MCP sidecar
- Routage Traefik (2 sous-domaines)
- SSH, mise toolchains, persistence des extensions
- MCP pré-câblé pour browser automation

### Layout "3 colonnes" dans un seul navigateur

**Option 1** — Deux onglets/fenêtres séparés (le plus simple)
- `code.monserveur.com` → code-server (arbre + éditeur)
- `ai.monserveur.com` → OpenCode Web (chat + diffs)

**Option 2** — Iframe wrapper (une seule page)
```html
<div style="display:flex; height:100vh">
  <iframe src="https://code.monserveur.com" style="flex:2"></iframe>
  <iframe src="https://ai.monserveur.com" style="flex:1"></iframe>
</div>
```

**Option 3** — Extension VS Code "OpenCode Web Sidebar"
- [opencode-web-sidebar](https://marketplace.visualstudio.com/items?itemName=bmpenuelas.opencode-web-sidebar) rend le chat OpenCode directement dans le panneau latéral de code-server.
- Résultat : un seul onglet, layout natif VS Code.

### Setup minimal

```bash
# docker-compose.yml
services:
  workspace:
    image: ghcr.io/joostme/opencode-docker:latest
    environment:
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - OPENCODE_SERVER_PASSWORD=${PASSWORD}
      - CODE_SERVER_PASSWORD=${PASSWORD}
    volumes:
      - ./repos:/repos
      - ./config:/home/coder/.config
      - ./share:/home/coder/.local/share
    ports:
      - "4096:4096"  # OpenCode Web
      - "8080:8080"  # code-server
```

### Avantages

- ✅ Prêt à l'emploi (image Docker existante)
- ✅ OpenCode supporte 75+ providers LLM (Anthropic, OpenAI, Ollama local, etc.)
- ✅ Filesystem partagé → l'agent voit et modifie les mêmes fichiers que l'éditeur
- ✅ MCP natif (outils custom, browser automation, etc.)
- ✅ Fonctionne sur un VPS à 5€/mois ou un Raspberry Pi
- ✅ Auth par mot de passe intégrée
- ✅ Supporte Tailscale, Authelia, Traefik pour la sécurité

### Inconvénients

- ⚠️ Pas un layout unique "3 colonnes" natif (sauf avec l'extension sidebar)
- ⚠️ OpenCode Web est encore jeune (bugs file tree rapportés)
- ⚠️ Nécessite une clé API LLM (ou Ollama sur la même machine)

---

## 3. Approche B — wede (WEb IDE)

### Principe

**[wede](https://github.com/webcrft/wede)** est un IDE web collaboratif en un seul binaire Go (~19 MB) :
- File explorer ✅
- Éditeur Monaco (comme VS Code) ✅
- Terminal partagé ✅
- Git intégré ✅
- Chat par workspace ✅ (mais pas un chat AI — c'est un chat humain-humain)
- Collaboration multi-utilisateurs (CRDT) ✅

### Ce qui manque : le panneau AI

wede n'a pas de chat AI intégré. Solutions :

1. **OpenCode en sidecar** : lancer `opencode web` à côté de wede, intégrer via iframe
2. **Open WebUI** (126k+ ★) comme panneau de chat connecté à Ollama/Anthropic
3. **Développer un plugin** : wede est Go, l'API est simple

### Architecture

```
┌────────────────────────────────────────────────────────┐
│                   wede (binaire Go)                      │
├───────────┬────────────────┬──────────┬────────────────┤
│ File Tree │    Monaco      │ Terminal │   Chat (human)  │
└───────────┴────────────────┴──────────┴────────────────┘
                                              +
                            ┌─────────────────────────┐
                            │  OpenCode Web (iframe)  │
                            │  ou Open WebUI          │
                            └─────────────────────────┘
```

### Avantages

- ✅ Un seul binaire, zéro dépendance
- ✅ Collaboration temps réel native
- ✅ Chat par workspace (persiste dans `.wede/chat.md`)
- ✅ Léger (tourne sur un Pi)

### Inconvénients

- ⚠️ Pas de chat AI natif — nécessite un sidecar
- ⚠️ Projet récent (avril 2025), communauté petite
- ⚠️ L'intégration AI reste à construire

---

## 4. Approche C — Eclipse Theia

### Principe

**[Eclipse Theia](https://theia-ide.org/)** est un framework pour construire des IDE web/desktop. C'est ce qui propulse Gitpod, Google Cloud Shell, Arduino IDE, etc.

- File tree ✅ (natif)
- Éditeur Monaco ✅ (natif)
- Terminal ✅ (natif)
- Extensions VS Code compatibles ✅
- Panneau AI : via [Theia AI](https://theia-ide.org/docs/) (intégré depuis 2025)

### Architecture

```
┌──────────────────────────────────────────────────┐
│              Eclipse Theia (Node.js)              │
├──────────┬─────────────────┬─────────────────────┤
│File Tree │  Monaco Editor  │   AI Chat Panel     │
│          │                 │  (Theia AI / custom) │
└──────────┴─────────────────┴─────────────────────┘
```

### Avantages

- ✅ Framework le plus mature pour IDE web custom
- ✅ Gouverné par Eclipse Foundation (pérennité)
- ✅ Extensions VS Code marketplace (partiellement)
- ✅ Theia AI intégré pour chat dans le panneau

### Inconvénients

- ❌ **Complexité** : monter un Theia custom demande Node.js, yarn, compilation
- ❌ Build lourd (~1h première fois)
- ❌ Over-engineered pour "juste" arbre + éditeur + chat
- ❌ Theia AI est encore en beta, documentation limitée
- ⚠️ Pas de "binaire unique" — c'est un framework, pas un produit fini

---

## 5. Approche D — Custom from scratch

### Stack technique

| Composant | Librairie open source |
|-----------|-----------------------|
| File Tree | Custom React tree / [react-arborist](https://github.com/brimdata/react-arborist) |
| Éditeur | [Monaco Editor](https://github.com/microsoft/monaco-editor) |
| Terminal | [xterm.js](https://github.com/xtermjs/xterm.js) |
| Chat AI | Composant React custom + streaming SSE |
| Backend | Node.js / Go (filesystem API + PTY + WebSocket) |
| LSP | [monaco-languageclient](https://github.com/TypeFox/monaco-languageclient) |
| AI | OpenCode API / Ollama API / Anthropic API direct |

### Références d'implémentation

- [iam-abdul/react_monaco_xtermjs_web_ide_with_LSP](https://github.com/iam-abdul/react_monaco_xtermjs_web_ide_with_LSP)
- [gitpod-io/xterm-web-ide](https://github.com/gitpod-io/xterm-web-ide)
- L'article Medium "[How to build a web IDE](https://levelup.gitconnected.com/how-to-build-a-web-ide-ab2563f24647)"

### Avantages

- ✅ Contrôle total du layout et de l'UX
- ✅ Léger si bien fait
- ✅ Intégration AI exactement comme tu veux

### Inconvénients

- ❌ **Effort colossal** : 3-6 mois pour un MVP fonctionnel
- ❌ Maintenance LSP, git, terminal, auth, sécurité
- ❌ Réinventer la roue (code-server fait déjà tout ça)

---

## 6. Approche E — Hermes Agent (NousResearch)

### Principe

**[Hermes Agent](https://hermes-agent.nousresearch.com/)** est un agent AI autonome (terminal, fichiers, browser, mémoire). Plusieurs UIs web existent :

- **[Hermes Studio](https://github.com/JPeetz/Hermes-Studio)** : dashboard complet (chat, terminal, file browser, skills, cron, MCP)
- **[hermes-workspace](https://github.com/outsourc-e/hermes-workspace)** : workspace natif avec chat + sessions
- **[hermes-control-interface](https://github.com/xaspx/hermes-control-interface)** : terminal + file explorer + sessions
- **Open WebUI** : frontend chat officiel recommandé par NousResearch

### Layout possible

```
┌─────────────────────────────────────────────────────┐
│              Hermes Studio / Workspace               │
├──────────┬────────────────────┬─────────────────────┤
│File Expl.│ Terminal / Editor   │    Agent Chat       │
│          │ (web terminal)     │  (streaming + tools) │
└──────────┴────────────────────┴─────────────────────┘
```

### Avantages

- ✅ Agent AI très puissant (mémoire, skills, browser, cron)
- ✅ Self-hosted nativement
- ✅ Supporte Nous Portal (300+ modèles) ou providers custom
- ✅ File editing + terminal intégrés dans l'agent
- ✅ MCP natif

### Inconvénients

- ⚠️ Pas un "vrai" éditeur de code (pas Monaco, pas de LSP, pas d'IntelliSense)
- ⚠️ Le file browser est basique (navigation, pas édition inline)
- ⚠️ Hermès est orienté "agent autonome" plus qu'"IDE"
- ⚠️ Hermes Studio/Workspace sont des projets communautaires (pas NousResearch officiel)
- ⚠️ Pour un vrai éditeur, il faudrait coupler avec code-server quand même

---

## 7. Comparaison synthétique

| Critère | A (OpenCode+CS) | B (wede) | C (Theia) | D (Custom) | E (Hermes) |
|---------|:---:|:---:|:---:|:---:|:---:|
| Arbre fichiers | ✅ natif | ✅ natif | ✅ natif | 🔨 à construire | ⚠️ basique |
| Éditeur riche (LSP) | ✅ VS Code | ✅ Monaco | ✅ Monaco | 🔨 Monaco | ❌ |
| Chat AI | ✅ OpenCode | 🔨 sidecar | 🔨 Theia AI | 🔨 custom | ✅ natif |
| Terminal | ✅ natif | ✅ natif | ✅ natif | 🔨 xterm.js | ✅ natif |
| Self-hostable | ✅ Docker | ✅ binaire | ✅ Docker | ✅ | ✅ Docker |
| Temps de setup | 10 min | 5 min + sidecar | 2-4h | 3-6 mois | 30 min |
| Multi-provider AI | ✅ 75+ | dépend sidecar | configurable | configurable | ✅ 300+ |
| Collaboration | ❌ | ✅ CRDT | ❌ | 🔨 | ❌ |
| Maturité | 🟢 haute | 🟡 récent | 🟢 haute | N/A | 🟡 moyen |

---

## 8. Recommandation finale

### Pour ton cas d'usage (dev solo, self-hosted, AI-first) :

### 🥇 Approche A — `opencode-docker` (ou variante maison)

**Setup en 10 minutes :**

```bash
git clone https://github.com/joostme/opencode-docker
cd opencode-docker
cp .env.example .env
# Éditer .env : ANTHROPIC_API_KEY, mots de passe, domaines

mkdir -p repos share config
docker compose up -d
```

**Pour le layout 3 colonnes dans un seul écran :**
1. Installer l'extension **opencode-web-sidebar** dans code-server
2. Ou utiliser un simple reverse-proxy Caddy/Traefik avec un wrapper HTML iframes

**Bonus Ollama (100% local, zéro API cloud) :**
```yaml
services:
  ollama:
    image: ollama/ollama
    volumes:
      - ollama_data:/root/.ollama
    # GPU passthrough si dispo
```
Puis dans `opencode.json` pointer vers `http://ollama:11434`.

### 🥈 Alternative si tu veux la collab : wede + OpenCode sidecar

Si tu veux inviter d'autres personnes à coder en même temps, wede est unique (CRDT natif, curseurs partagés, terminaux partagés). L'AI se branche en sidecar.

### 🥉 Si déjà sur Hermès : Hermes Studio + code-server à côté

Tu as déjà un agent Hermès qui tourne (Léo). Coupler Hermes Studio pour le chat agent + code-server pour l'éditeur donne un setup cohérent.

---

## 9. Points d'attention

| Sujet | Conseil |
|-------|---------|
| **Sécurité** | Toujours derrière Authelia/Tailscale. Jamais exposer code-server nu sur internet. |
| **Performance** | code-server + OpenCode = ~512 Mo RAM idle. Prévoir 2 Go+ pour un usage confortable. |
| **GPU / LLM local** | Si Ollama, prévoir un VPS avec GPU ou un desktop avec une RTX. Sinon, API cloud. |
| **Backup** | Monter `/repos` sur un volume Docker persistant + backup régulier. |
| **Mobile** | OpenCode Web est touch-friendly. code-server moins. |

---

## 10. Liens utiles

- [OpenCode - site officiel](https://opencode.ai/) — 150k+ ★, MIT
- [OpenCode Web docs](https://opencode.ai/docs/web/)
- [code-server](https://github.com/coder/code-server) — 70k+ ★, MIT
- [opencode-docker](https://github.com/joostme/opencode-docker) — combo prêt à l'emploi
- [wede](https://github.com/webcrft/wede) — IDE web collaboratif, single binary Go
- [Eclipse Theia](https://theia-ide.org/) — framework IDE web extensible
- [Hermes Agent](https://hermes-agent.nousresearch.com/) — agent AI autonome
- [Hermes Studio](https://github.com/JPeetz/Hermes-Studio) — web UI pour Hermes
- [Open WebUI](https://openwebui.com/) — 126k+ ★, interface chat self-hosted
- [Monaco Editor](https://github.com/microsoft/monaco-editor) — éditeur de VS Code
- [xterm.js](https://github.com/xtermjs/xterm.js) — terminal web

---

*Étude réalisée le 22 août 2026. Sources vérifiées à cette date.*
