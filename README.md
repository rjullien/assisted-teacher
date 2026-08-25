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
| `HERMES_FS_PREFIXES` | `/opt/data/home/,/opt/data/` | Points de montage du disque de Lya, retirés des chemins absolus de ses frames d'outil avant recopie dans `WORKSPACE_DIR` |
| `HERMES_TRACE_EVENTS` | `false` | Trace brute de **toutes** les frames SSE reçues de Hermes (préfixe `hermes_trace:`, payload coupé à 2000 car.). Diagnostic temporaire : les payloads contiennent le cours |
| `PI_ENABLED` | `true` | Active le mode Pi. À `false` : pas de bouton Pi, pas de route `/ws/agent/pi` |
| `PI_CMD` | `pi` | Binaire pi (installé dans l'image) |
| `PI_PROVIDER` | `bifrost` | Clé de provider dans `~/.pi/agent/models.json` |
| `PI_MODEL` | `bifrost-default` | Champ `name` du modèle, **pas** son `id` |
| `PI_MODEL_ID` | `opencode-go/deepseek-v4-flash` | Identifiant envoyé à Bifrost |
| `BIFROST_URL` | `http://bifrost.openclaw.svc.cluster.local:8080/v1` | Endpoint LLM de pi |
| `BIFROST_API_KEY` | — | Optionnelle : Bifrost est sans auth depuis le cluster |
| `BRAVE_SEARCH_API_KEY` | — | Clé Brave Search, partagée par les 3 modes. Absente : aucune recherche web n'est proposée (voir [Recherche web](#recherche-web-brave-search)) |

### Auth vers Lya (Hermès)

Le backend n'utilise plus de subprocess ACP : il appelle Lya en HTTP streaming (`POST /v1/chat/completions` + `Authorization: Bearer …`).

Si le chat affiche `Échec IA` avec un détail `hermes auth failed (HTTP 401)` / `Invalid gateway API key`, ce n'est **pas** Authelia : c'est la clé gateway Hermes.

**Piège prod (vérifié Tailscale/k3s) :** Hermes valide `API_SERVER_KEY` depuis le fichier PVC `/opt/data/.env`, **pas** depuis l'env K8s/Infisical injectée dans le pod. Les deux peuvent diverger après un rotate Infisical. Alignement :

1. Infisical `hermes-lya-secret.API_SERVER_KEY` == `assisted-teacher-secret.API_SERVER_KEY` (== `HERMES_API_KEY`)
2. **et** `/opt/data/.env` `API_SERVER_KEY` sur le PVC `hermes-lya-data` == la même valeur
3. puis `kubectl -n openclaw rollout restart deploy/hermes-lya`

Même pattern possible sur `hermes-leo` / TripKit.

### Les trois modes

| Mode | Écran | Agent | Fichiers | Recherche web |
|---|---|---|---|---|
| **Desk** | 3 panneaux | Hermes/Lya via `/ws/acp` | L'enseignante écrit ; « Insérer » pour reprendre une réponse | outil `web_search` |
| **Pi** | les mêmes 3 panneaux | pi via `/ws/agent/pi` | **pi lit et écrit les fichiers lui-même** | outil `web_search` (extension pi) |
| **Lya** | plein écran | Hermes/Lya via `/ws/acp` | aucun | outil `web_search` |

Le mode Pi n'apparaît que si le serveur l'annonce via `GET /api/agents`. Un mode mémorisé mais indisponible retombe sur Desk.

### Configuration de pi

`entrypoint.sh` écrit `models.json` et `settings.json` dans `$HOME/.pi/agent/` au démarrage — **le seul endroit où pi les lit**. Il n'existe pas de variable d'environnement pour pointer ailleurs.

Outils : `--tools read,edit,write,grep,find,ls,web_search`. Les six premiers sont des outils intégrés de pi ; `web_search` vient de l'extension `backend/pi-config/extensions/brave-search`. Doublée par `defaultTools` dans `settings.json` pour les intégrés. `TestPi_ToolAllowlistIsExact` épingle la chaîne, parce que `--tools` **n'est pas validé par pi** : `--tools read,jenexistepas` est accepté en silence, donc une faute de frappe désactiverait un outil sans aucune erreur au démarrage.

**`bash` reste exclu.** `cmd.Dir` et `safePath()` encadrent les **outils fichiers**, pas un sous-processus : un shell rendrait la prison du workspace indicative. La recherche web n'a pas besoin de shell parce que pi expose une **API d'outils personnalisés**, `pi.registerTool()` ([docs/extensions.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md)), présente en 0.84.2. L'extension appelle Brave depuis le process de pi.

**`web_search` doit figurer dans `--tools`.** Mesuré sur 0.84.2 : `--tools` est un allowlist strict sur **tous** les outils, extensions comprises. En l'omettant, l'extension se charge sans erreur mais pi ne déclare jamais l'outil au modèle. Liste réellement déclarée, relevée sur l'image :

```
edit, find, grep, ls, read, web_search, write
```

Les autres garde-fous inchangés : `--no-approve` et `defaultProjectTrust: never` empêchent d'honorer un `.pi/` déposé par l'enseignante, `--no-context-files` garde un `AGENTS.md` déposé dans le workspace hors du prompt.

### Recherche web (Brave Search)

Une seule variable, `BRAVE_SEARCH_API_KEY`, pour les trois modes — mais deux chemins techniques, parce que Lya et pi ne s'exécutent pas au même endroit.

Ce nom est celui qu'impose l'image tierce `nousresearch/hermes-agent`, qui porte aussi les pods `hermes-lya` et `hermes-leo`. Comme cette image n'est pas modifiable et que ce dépôt l'est, c'est ce dépôt qui s'aligne : une seule entrée Infisical alimente tout le cluster, et `grep -r BRAVE_SEARCH_API_KEY` retrouve la chaîne de bout en bout.

| Mode | Mécanisme | Ce que voit l'enseignante |
|---|---|---|
| **Desk**, **Lya** | Le backend déclare un outil `web_search` dans la requête `/v1/responses` et exécute l'appel Brave lui-même (`internal/bridge/brave.go`) | `🔍 Recherche web : <termes>` dans le fil |
| **Pi** | La clé (trimmée) est exportée au sous-processus, et l'extension `brave-search` enregistre un outil `web_search` qui appelle Brave depuis le process de pi | l'appel apparaît comme un outil `web_search` |

Points à connaître :

- **Pas de clé, pas d'outil.** L'outil `web_search` n'est pas déclaré à Lya et n'est pas annoncé à pi. Les deux répondent alors sans recherche, plutôt que d'échouer sur un 422 au milieu d'une réponse.
- **Un échec Brave n'est jamais une erreur de job.** Timeout, 429, clé invalide : le résultat d'outil dit à l'agent de chercher avec ses propres moyens et la réponse continue. C'est pour ça que tous les messages d'erreur de `brave.go` finissent par la même phrase.
- **`web_search` est en lecture seule**, donc proposé dans **tous** les modes, y compris `lya` et le sous-mode « copie/insertion » où aucun outil fichier n'est armé. Un appel à un outil fichier dans ces modes reste refusé (`TestHermesBridge_WebSearch_AllowedInLyaModeButWriteStillRefused`).
- L'extension pi vit dans `backend/pi-config/extensions/brave-search/` et est copiée dans `$HOME/.pi/agent/extensions/` par `entrypoint.sh`. Pi la charge via jiti : pas d'étape de compilation TypeScript, et pas de `node_modules` à embarquer puisque pi fournit `typebox`. Vérifier qu'elle est bien chargée :

```bash
kubectl -n assisted-teacher logs deploy/assisted-teacher-backend | grep -E 'pi: extensions|pi: web search'
# pi: extensions installed: brave-search
# pi: web search enabled (BRAVE_SEARCH_API_KEY len=32)
```

- La clé est trimmée (`NewBraveSearch`, et `readApiKey` côté extension). Contrairement à ce qu'on pourrait croire, ce n'est pas pour protéger l'en-tête : vérifié, `undici` retire lui-même les espaces en fin de valeur d'en-tête, donc un `\n` ne casse pas la requête. Le trim sert à ce que le test de vacuité soit juste — une clé Infisical créée vide vaut `"\n"`, qui est truthy, et partirait en appel Brave pour récolter un 422 au lieu d'être traitée comme absente.

### Diagnostiquer les écritures de Lya (mode Desk / « Mise à jour directe »)

Symptôme : Lya répond « c'est fait, j'ai ajouté la ligne… », et le fichier de travail n'a pas changé. Trois versions (v1.9.0 interception des écritures, v1.10.0 outils déclarés, v1.10.1 mapping des chemins côté Hermes) ont été écrites contre une forme **supposée** de ses frames d'outil ; aucune vraie frame n'avait jamais été observée. Les commandes ci-dessous servent à établir les faits avant de tenter un quatrième correctif.

#### 0. Pourquoi ça ne marchait pas (v1.9.0 → v1.11.0)

Le bridge appelait `POST /v1/chat/completions`. Ses frames `hermes.tool.progress` ne portent **jamais** le contenu du fichier : en production comme dans le code de la gateway, elles ne contiennent que `{tool, emoji, label, toolCallId, status}` — statuts `running`/`completed`, **ni `path` ni `content`**. Toute garde de `handleToolFileWrite` fondée sur `status == "done"` ou sur des clés `path`/`content` rejetait donc **toutes** les frames, silencieusement.

Depuis v1.12.0, le bridge utilise **`POST /v1/responses`** (OpenAI Responses API), dont le flux émet `response.output_item.added` avec `item.type == "function_call"` et `arguments` **complets** (JSON string avec `path` + `content` pour `write_file`). Le mirroring se fait sur cette frame (une seule fois : le `done` répète les mêmes arguments et n'est pas re-mirroré). C'est le canal qui transporte réellement le contenu.

#### 1. Une frame d'outil est-elle arrivée, et sous quel nom ? (toujours actif)

```bash
kubectl logs -n assisted-teacher deploy/assisted-teacher-backend --since=30m \
  | grep -E "hermes: tool_calls supported=|hermes: tool frame"
```

La première ligne est le récapitulatif d'un job, la ou les suivantes décrivent chaque frame d'outil reçue :

```
hermes: tool_calls supported=false (mode=desk deskMode=direct loops=1 toolCalls=0 mirroredWrites=0 skippedWrites=0 sseEventFrames=1 sseEventNames=[hermes.tool.progress] toolProgressFrames=1 sseChunkFrames=42)
hermes: tool frame seen (name="write_file" status="running" pathFound=true contentFound=false keys=[name,path,status])
hermes: tool frame not mirrored (write tool "write_file" reported status "running", not "done")
```

Comment lire le récapitulatif :

| Ce qu'on lit | Ce que ça veut dire | Suite |
|---|---|---|
| `sseEventFrames=0` | Lya n'a envoyé **aucune** frame d'événement : elle a répondu en texte, sans jamais signaler d'activité d'outil | Le problème est en amont du backend (prompt, gateway, modèle) — rien dans `handleToolFileWrite` ne peut y changer quoi que ce soit |
| `toolProgressFrames=0` avec `sseEventFrames>0` | Elle signale son activité sous un **autre nom d'événement**, listé dans `sseEventNames=[…]` | La constante `hermesToolProgressEvent` (backend/internal/bridge/hermes.go) est le bug |
| `toolProgressFrames>0` + une ligne `not mirrored (…)` | La frame est arrivée et un garde l'a refusée ; la ligne dit lequel et avec quel nom d'outil | Corriger ce garde-là, avec le nom réellement vu |
| `mirroredWrites>0` | Le fichier a bien été écrit dans `WORKSPACE_DIR` | Chercher côté frontend (rechargement de l'éditeur) |
| `supported=true` | La gateway transmet bien le paramètre `tools` : Lya utilise les outils déclarés (`read_file`/`write_file`/`patch_file`), pas les siens | — |

`toolProgressFrames=0` et « une frame refusée » étaient indistinguables avant : les deux ne laissaient aucune trace, pour un symptôme identique côté enseignante.

#### 2. Frames SSE brutes (`HERMES_TRACE_EVENTS`)

Quand le récapitulatif ne suffit pas — typiquement pour voir les **clés** réelles d'une frame — activer la trace. ArgoCD a `selfHeal: true` sur cette application : un `kubectl set env` est annulé en quelques minutes, la variable doit donc être ajoutée dans `vps-infra/workloads/assisted-teacher/backend/deployment.yaml` :

```yaml
            - name: HERMES_TRACE_EVENTS
              value: "true"
```

Puis, une fois le pod redéployé, **la commande à lancer** :

```bash
kubectl logs -n assisted-teacher deploy/assisted-teacher-backend -f | grep "hermes_trace:"
```

Chaque frame reçue donne une ligne `hermes_trace: event=<nom|<none>> data=<payload>` (payload coupé à 2000 caractères, marqueur `…[truncated: 2000 of N chars]`). Les deltas de texte sont comptés mais un seul est tracé par tour, sinon ils enterrent tout le reste.

⚠️ À retirer après le diagnostic : les payloads contiennent le contenu des cours. Ni la clé API ni l'en-tête `Authorization` n'apparaissent jamais (ils ne font pas partie d'une frame ; tout ce qui touche à la clé passe par son empreinte SHA-256, cf. `keyFp=`).

#### 3. Capturer une frame sans passer par le backend

```bash
HERMES_URL=http://hermes-lya.openclaw.svc.cluster.local:8642 HERMES_API_KEY=… \
  ./scripts/capture-hermes-frames.sh
```

Le script poste le prompt de l'incident, écrit le flux SSE brut dans un fichier et affiche les noms d'événements vus puis les clés de la première frame d'outil. Il n'affiche jamais la clé, seulement son empreinte (la même que la ligne `hermes: bridge configured … keyFp=…`). Nécessite un accès réseau à `$HERMES_URL` (Tailscale ou depuis un pod du cluster).

⚠️ Ne **pas** ajouter de nom d'outil dans `writeToolNames` au feeling : la ligne `is not a known write tool` affiche le nom réellement reçu, c'est lui qu'il faut ajouter, et le test `TestKnownWriteToolNames` échoue exprès pour forcer ce constat.

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
