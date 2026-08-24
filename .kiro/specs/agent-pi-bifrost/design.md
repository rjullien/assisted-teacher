# Second agent : pi + Bifrost (design)

> Design technique. Le « quoi » et le « pourquoi » sont dans [requirements.md](requirements.md).
> Statut : specification. Aucun fichier de `backend/`, `frontend/`, `docker-compose.yml`, `.env.example`, `version.json` ni `.github/` n'est modifié par ce document. Il décrit des changements pour une tâche ultérieure.
> Tout identifiant précédé de la mention **(nouveau)** n'existe pas encore dans le dépôt.

---

## 🧭 Décisions

| # | Question | Décision | Alternatives écartées | Motif |
|---|----------|----------|-----------------------|-------|
| D1 | Quel harnais pour le second agent ? | `pi`, paquet `@earendil-works/pi-coding-agent` épinglé en `0.84.2`, piloté par `pi --mode rpc` | opencode, OpenClaw, Kiro CLI, Claude Code ACP, Gemini CLI, Codex CLI, crush, goose, et pi via un adaptateur ACP tiers | Aucun SDK ACP **officiel** en Go ([libraries](https://agentclientprotocol.com/libraries/typescript) : TypeScript, Rust, Python, Kotlin, Java). Le mode RPC de pi est du JSONL délimité par `\n` sur un tube, lisible avec l'idiome `bufio.Scanner` déjà présent dans `callHermesStream` (`backend/internal/bridge/hermes.go`) |
| D2 | Implémente-t-on ACP ? | Non. pi parle son propre protocole JSONL. La promesse ACP est retirée | Écrire un client ACP JSON-RPC en Go ; utiliser un SDK ACP Go communautaire | `ACP_AGENT_CMD`, `OPENCODE_BASE_URL`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` sont lus par **zéro** ligne de Go ou de TypeScript : la promesse n'a jamais été construite. Un SDK communautaire ajouterait une dépendance non officielle sur le chemin critique |
| D3 | Où vit le second agent dans le backend ? | Nouveau fichier **(nouveau)** `backend/internal/bridge/pi.go` **dans le package `bridge` existant**, réutilisant `StreamEvent`, `Job`, `Hub`, `Subscribe(after)`, `jobTTL`, `jobRunTimeout` et `keyFingerprint` tels quels | Extraire la machinerie de job dans un package `agent` derrière une interface `Agent` + registre ; agent unique choisi au démarrage par variable d'environnement ; `if/else` dans `HermesBridge` | Le journal append-only à `Seq` monotone et le `Subscribe` « backlog puis live » sont le code le plus délicat du dépôt et les seuls couverts par des tests dans ce package (3 tests verts). On ne le refactore pas pour deux implémentations. Un agent choisi au démarrage tuerait le sélecteur, qui est tout l'objet du travail |
| D4 | Quelle route ? | **(nouveau)** `/ws/agent/pi`, motif nu sans préfixe de méthode, enregistré uniquement si pi est configuré. `/ws/acp` conservé tel quel | Multiplexer sur `/ws/acp` avec un champ `agent` dans le message ; paramètre `?agent=` | `main.go` enregistre déjà `/ws/acp` conditionnellement (`if *hermesKey != ""`) : même idiome. Deux routes distinctes rendent EX-4 et EX-5 vérifiables par construction |
| D5 | Comment le frontend sait-il quels agents existent ? | **(nouveau)** `GET /api/agents` renvoyant `[{id, label, default}]` | Constante compilée dans le bundle ; sonde de la route WebSocket | EX-3 : l'interface ne doit jamais proposer un agent non configuré, et le bundle peut être en cache pendant des semaines. Seul le serveur connaît son état |
| D6 | Cycle de vie du processus pi | Un processus par demande, propriété du `Job`, terminé par la fin de la réponse ou par `jobRunTimeout` (10 min) | Processus long-lived avec `--session` / `-c` ; processus propriété de la connexion WebSocket | La promesse détachée existante (« un débranchement du frontend n'annule pas le job ») serait cassée si le processus appartenait à la connexion. Un processus par demande a exactement la durée de vie déjà bornée par `jobRunTimeout` |
| D7 | Comment Bifrost est-il câblé ? | Fournisseur nommé `bifrost` déclaré dans `models.json`, fichier **rendu au démarrage** avec la clé débarrassée de ses espaces. ⚠️ Rendu par `entrypoint.sh` et non par le Go à l'implémentation, et la clé de fournisseur est `bifrost`, pas `custom` — voir « Écarts constatés » | Interpolation `"$BIFROST_API_KEY"` par pi ; forme `"!command"` (gardée en repli) | Les secrets livrés depuis Infisical vers Kubernetes portent régulièrement un `\n` final ; `main.go` et `NewHermesBridge` s'en protègent déjà par `strings.TrimSpace`. L'interpolation de `models.json` ne fait aucun `trim` : le `\n` finirait dans l'en-tête `Authorization: Bearer` |
| D8 | Comment le pilote choisit-il ? | **Trois modes de premier niveau** dans le sélecteur existant : `AppMode = 'desk' \| 'pi' \| 'lya'`. Aucun second sélecteur | Sélecteur d'agent imbriqué dans le mode Desk (**version précédente de ce document**) ; sélecteur global visible aussi en mode Lya | Un sélecteur d'agent imbriqué fabrique une matrice 2×2 dont une case est morte (Lya × pi) et un contrôle qui apparaît et disparaît selon le mode. Trois boutons sur une seule ligne rendent la combinaison invalide **irreprésentable** au lieu de devoir l'expliquer, et réutilisent `MODE_STORAGE_KEY`, `loadMode/saveMode`, `toolbar-mode-switcher` et le groupe i18n `mode` tels quels : c'est **moins** de code que la version qu'elle remplace. Détail en 7.1 |
| D9 | Prompt système pédagogique | MVP : envoyé comme **tour de préambule** par le backend, en réutilisant le champ `system` déjà transmis par `Chat.tsx`. v2 : skill pi monté dans l'image | Skill statique dans l'image dès le MVP ; `--system-prompt` ; `.pi/SYSTEM.md` | `buildSystemPrompt()` dépend du niveau choisi **à l'exécution** (données de `/api/programme`). Un skill statique ne peut pas suivre le niveau sans être régénéré, et un fichier `.pi/SYSTEM.md` posé dans le workspace serait une ressource de projet donc soumise au trust (voir D13) |
| D10 | Outil `bash` | **Exclu**. Allowlist : `read,edit,write,grep,find,ls`, posée à la fois par `defaultTools` dans `settings.json` et par `--tools` (allowlist stricte pour *tous* les outils). ⚠️ La clé `tools.allowlist` employée d'abord n'existe pas et ne restreignait rien — voir « Écarts constatés » | Inclure `bash` avec des garde-fous applicatifs | Un assistant de rédaction de cours n'a aucun besoin de shell, et `bash` est le seul outil qu'aucune frontière de chemin ne peut contenir. C'est la décision la plus importante du document |
| D11 | Historique de conversation | Par agent, sans rejeu croisé. Le fil affiché est conservé à l'écran mais n'est pas renvoyé à l'autre agent | Historique partagé et rejoué vers l'agent sélectionné | Le protocole actuel est déjà sans historique : `Chat.tsx` envoie `{type:'prompt', content, system}` et rien d'autre. Partager un historique serait une fonctionnalité nouvelle, non demandée, et rendrait la comparaison du pilote impure |
| D12 | Emballage | **Même conteneur** que le backend Go : `alpine:3.22`, `nodejs` + `npm`, paquet pi épinglé | Sidecar pi ; service Node séparé embarquant le SDK | `pi --mode rpc` est stdio seulement, sans écouteur réseau : un sidecar exige un adaptateur stdio-vers-réseau, et `@earendil-works/pi-server` est auto-déclaré « experimental ». Variantes conservées comme v2 avec leurs déclencheurs (9.3) |
| D13 | Confiance projet (`defaultProjectTrust`) | Fixée **explicitement** à `never` dans `settings.json`, plus `--no-approve` et `--no-context-files` par exécution | Laisser la valeur par défaut `ask` | Les modes non interactifs n'affichent aucune invite : le comportement dépend d'un réglage global. Le workspace étant **inscriptible par l'enseignant** via `PUT /api/file`, c'est une entrée non fiable. ✅ Décision confirmée par la mesure : sans `--no-context-files`, un `AGENTS.md` du workspace atteint le modèle | Laisser la valeur par défaut `ask` | Les modes non interactifs (`-p`, `--mode json`, `--mode rpc`) n'affichent aucune invite : le comportement dépend d'un réglage global. Or le workspace est **inscriptible par l'enseignant** via `PUT /api/file` : c'est une entrée non fiable pour pi. Un réglage implicite n'est pas acceptable ici |
| D14 | Mesure de l'usage | Une ligne de journal structurée par demande, via `log.Printf`, dans le même flux que les lignes `hermes:` | Base de données, métriques Prometheus, télémétrie frontend dédiée | Zéro infrastructure nouvelle, `grep`-able, et le signal économique complémentaire existe déjà côté Bifrost via le tableau de bord relié depuis `Toolbar.tsx` |

---

## ⚠️ Écarts constatés à l'implémentation

Cette section est ajoutée **après** la livraison (v1.8.0). Les décisions ci-dessus ont été prises sur lecture de la documentation ; l'implémentation a ensuite été confrontée à un pi 0.84.2 réellement installé, et plusieurs points se sont révélés faux. Ils sont corrigés ici plutôt que laissés en l'état, pour que ce document reste un compte rendu utilisable.

**Méthode de vérification** : pi 0.84.2 installé dans un sandbox (Node 22), plus un end-to-end contre un faux serveur OpenAI local — ce qui valide toute la chaîne (appel LLM → tool call → écriture de fichier) sans consommer un seul jeton.

| Point | Ce que disait la spec | Réalité vérifiée |
|---|---|---|
| **Cause du plantage initial** | — | `--provider custom` fait sortir pi en **1** avec `Error: Unknown provider "custom"`. `custom` n'est pas un nom de fournisseur : c'est la clé qu'on choisit soi-même dans `models.json`. La clé retenue est `bifrost` |
| **Schéma de `models.json`** | non détaillé | `{ "providers": { "<clé>": { "baseUrl", "api", "apiKey", "authHeader", "models": [{ "id", "name" }] } } }`. `api` vaut `openai-completions` |
| **Sélection du modèle** | `--model` non discuté | `--model` cible le champ **`name`**, pas l'`id`. L'`id` (`opencode-go/deepseek-v4-flash`) contient une barre oblique que `--model` interpréterait comme un couple `provider/id`. D'où un `name` sans barre oblique : `bifrost-default` |
| **Qui rend `models.json`** | « rendu au démarrage par le **backend Go** » (D7) | Rendu par `entrypoint.sh` avant l'exec du binaire Go. Le `trim` de la clé est fait en shell (`tr -d '[:space:]'`), l'intention de D7 est respectée |
| **Restriction des outils** | `settings.json` (D10) | La clé `tools.allowlist` employée d'abord **n'existe pas** et ne restreignait donc rien : `bash` était disponible. Le champ réel est **`defaultTools`**, et `--tools` est en plus une allowlist stricte pour *tous* les outils. Les deux sont désormais posés |
| **`--no-context-files`** | listé en D13 et 8c comme protection | **La spec avait raison, l'implémentation a eu tort de le retirer.** Le drapeau est absent de la documentation publiée mais bien fonctionnel. Mesuré contre pi 0.84.2 avec un serveur LLM enregistreur : sans lui, un marqueur placé dans `workspace/AGENTS.md` **atteint le prompt envoyé au modèle** ; avec lui, non. `defaultProjectTrust: never` ne couvre pas les fichiers de contexte, qui sont chargés indépendamment de la confiance projet. Retiré à tort en v1.8.0, rétabli en v1.8.1, verrouillé par `TestPi_DisablesWorkspaceContextFiles` qui assère l'argv réel |
| **Emplacement de la config** | `PI_CODING_AGENT_DIR` (§9.4) | **La spec avait raison et l'implémentation ne l'a pas suivi.** La variable existe bien (`pi --help` : « Config directory (default: ~/.pi/agent) ») et a été vérifiée avec contrôle négatif. L'implémentation écrit dans `$HOME/.pi/agent`, ce qui fonctionne mais dépend d'un `HOME` inscriptible — incompatible avec l'objectif d'uid non privilégié de §8. À reprendre |
| **Appels réseau au démarrage** | non prévu | pi contacte `pi.dev` au démarrage (vérification de version, télémétrie d'installation). Dans le cluster ces appels échouent ou pendent : `PI_OFFLINE=1` et `PI_SKIP_VERSION_CHECK=1` sont désormais posés |
| **Cycle de vie de stdin** | non prévu | Fermer stdin juste après avoir écrit le prompt fait sortir pi **avant** l'aller-retour LLM (exit 0, aucun appel au modèle). Le pont garde donc stdin ouvert jusqu'à la fin du tour |
| **Chemin des fichiers écrits** | non prévu | `tool_execution_end` porte `args: {}`. Le chemin n'existe que sur `tool_execution_start`, et doit être mémorisé par `toolCallId` — sans quoi aucun `file_changed` n'est émis et l'éditeur ne recharge jamais ce que pi vient d'écrire |
| **Diagnostic** | non prévu | Le pont ne câblait pas `StderrPipe`, donc le message d'erreur de pi était perdu et il ne restait qu'un code de sortie. stderr est maintenant journalisé et remonté dans l'interface |

**Ce que la §7 avait bien anticipé** : l'annexe de correspondance des événements est juste sur `message_update` / `assistantMessageEvent` / `text_delta`, sur `tool_execution_*`, et sur `agent_end`. Le protocole RPC lui-même n'était pas en cause.

**Leçon transposable, dans les deux sens** :

- Une décision de conception fondée sur une documentation lue reste une hypothèse. Chaque hypothèse non exécutée a coûté ici un cycle release + déploiement. Installer l'outil et reproduire l'échec donnait la cause en une commande.
- Symétriquement : **ne pas retirer une protection existante au motif qu'elle n'est pas documentée.** `--no-context-files` a été supprimé sur cette base, alors qu'il était fonctionnel et que 8c expliquait précisément le risque couvert. La bonne démarche était de mesurer ce que la protection empêche — ce qui a fini par être fait, et a rétabli le drapeau.

---

## 1. 🧰 Décision 1 : choix du harnais

### 1.1 L'argument décisif : le coût d'intégration en Go

Le backend est en Go (`backend/go.mod` : `go 1.23`, une seule dépendance, `github.com/gorilla/websocket`). Le choix du harnais se joue donc sur une question et une seule : combien de code faut-il écrire, et de quelle nature, pour piloter l'agent depuis Go ?

- **Par ACP**, il faut écrire à la main un client JSON-RPC : `initialize`, `session/create`, `prompt/start`, la réception des `notification/progress`, et le traitement de `client/requestPermission`. Les bibliothèques officielles listées par le protocole sont en TypeScript, Rust, Python, Kotlin et Java. Il existe des bibliothèques Go, mais uniquement dans la [page communautaire](https://agentclientprotocol.com/libraries/community) (`acp-go-sdk`, `acp-go`, `acp`, `acp-sdk`) : quatre implémentations tierces concurrentes, non officielles, à mettre sur le chemin critique d'un produit mono-mainteneur. Ce n'est pas un argument contre ACP en général, c'est un argument contre ACP **ici**.
- **Par le mode RPC de pi**, il faut lire des lignes JSON délimitées par `\n` sur `stdout` et écrire des lignes JSON sur `stdin`. Le dépôt contient déjà exactement cet idiome : `callHermesStream` lit le flux SSE de Hermes avec un `bufio.Scanner` dont le token maximal est porté à 2 Mio. Le pont pi est le même patron, avec un `os/exec` à la place d'un `http.Client`.

À cela s'ajoute une contrainte de forme utile : le mode RPC est spécifié en JSONL strict, `\n` comme seul délimiteur, avec l'avertissement explicite de ne pas utiliser de lecteur de lignes générique qui découperait aussi sur `U+2028`/`U+2029`. Un `bufio.Scanner` en Go ne découpe que sur `\n`, donc il est conforme par construction là où un `readline` Node ne l'est pas.

### 1.2 Les trois raisons secondaires

| Raison | Détail | Pourquoi cela compte ici |
|--------|--------|--------------------------|
| Gating d'outils au niveau CLI | `--tools`/`-t`, `--exclude-tools`/`-xt`, `--no-builtin-tools`/`-nbt`, `--no-tools`/`-nt`, sur `read`, `bash`, `edit`, `write`, `grep`, `find`, `ls` | `safePath()` (`backend/internal/api/files.go`) est **contourné** par un sous-processus qui écrit avec ses propres outils. L'allowlist CLI est le levier de confinement le plus direct disponible (D10, section 8) |
| Bifrost en configuration et non en code | Provider custom dans `models.json` ([docs/models.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md)) | Aucune ligne de Go à écrire pour changer de modèle ou d'URL de passerelle. Bifrost reste le point de sortie unique et le point de contrôle du budget |
| Sessions offertes | `--session-dir`, `--session`, `--fork`, `-c`/`--continue`, `--no-session` | S'aligne sur le modèle `Job` + rejeu déjà en place, et donne une piste d'audit lisible pour le diagnostic (section 5.4) |

### 1.3 Les harnais examinés et écartés pour ce créneau

| Harnais | Écarté parce que |
|---------|------------------|
| [opencode](https://github.com/sst/opencode) | S'intègre par un serveur à superviser plutôt que par un simple tube stdio : cela ajoute un service dans le conteneur, exactement ce que le second agent est censé éviter. C'est aussi le chemin que `OPENCODE_BASE_URL` promettait sans jamais l'implémenter |
| [OpenClaw](https://github.com/openclaw/openclaw) | Occupe le même créneau que Hermes/Lya (agent hébergé, mémoire, autonomie). Le retenir ne testerait pas l'hypothèse « plus léger », qui est la question du pilote |
| [Kiro CLI](https://kiro.dev) | Chaîne de distribution et identité externes mal adaptées à un embarquement dans une image publiée sur GHCR (à confirmer si le besoin revient) |
| [Claude Code ACP](https://www.npmjs.com/package/@zed-industries/claude-code-acp) | Impose le client ACP en Go **et** une clé Anthropic dans le conteneur, ce qui contredit « Bifrost seul point de sortie » (EX-11) |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | Même double coût : client ACP à écrire et fournisseur de modèle imposé hors Bifrost |
| [Codex CLI](https://github.com/openai/codex) | Idem : l'égress modèle sortirait de Bifrost, donc du point de contrôle du budget |
| [crush](https://github.com/charmbracelet/crush) | Écrit en Go, donc séduisant (aucun runtime Node à embarquer), mais destiné à un usage terminal interactif ; pas de mode RPC JSONL documenté équivalent à celui de pi (à confirmer) |
| [goose](https://github.com/block/goose) | Orienté extensions MCP et usage poste de travail : plus de surface à contraindre pour un besoin de rédaction de cours |
| pi via un adaptateur ACP tiers | Des adaptateurs existent sur npm (par exemple [`pi-acp`](https://github.com/svkozak/pi-acp)), mais ils rajoutent une couche non officielle **et** le client ACP en Go, pour arriver au même endroit que `--mode rpc` en direct |

Aucun nombre d'étoiles n'est cité ici : ce genre de chiffre vieillit mal et n'a pesé dans aucune de ces lignes.

### 1.4 ⚠️ Piège de nommage npm

| Nom | Réalité |
|-----|---------|
| `@earendil-works/pi-coding-agent` | **Le bon paquet.** Binaire `pi`, `engines.node >= 22.19.0` |
| `@mariozechner/pi` | Ancien nom, aujourd'hui un outil **sans rapport** de gestion de déploiements vLLM sur pods GPU, binaire `pi-pods` |
| `badlogic/pi-mono` | Ancien dépôt : redirige vers [`earendil-works/pi`](https://github.com/earendil-works/pi) |

La règle : n'installer et ne citer que `@earendil-works/*`, et **épingler la version**. Un `npm install -g` non épinglé dans un `Dockerfile` fait varier l'agent d'un build à l'autre.

### 1.5 Non-fonctionnalités assumées de pi

pi annonce explicitement ne pas faire : pas de MCP, pas de sous-agents, pas de fenêtres de permission, pas de mode plan.

| Non-fonctionnalité | Impact ici |
|--------------------|------------|
| Pas de MCP | **Aucun.** Aucun besoin de serveur MCP dans ce produit |
| Pas de sous-agents | **Aucun.** Une demande, une réponse |
| Pas de mode plan | **Aucun** pour de la rédaction de cours |
| Pas de fenêtres de permission | **Central.** Il n'y a rien à cliquer pour autoriser une écriture : la frontière doit être posée avant le lancement, par la liste d'outils, le répertoire de travail et le conteneur (section 8) |

---

## 2. 🪦 Décision 2 : retrait de la promesse ACP

`README.md` documente `ACP_AGENT_CMD`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` et `OPENCODE_BASE_URL`, et `docs/etude-faisabilite-ide-web/architecture-v2.md` décrit une architecture « ACP-native » avec un sous-processus agent. Un `grep` sur `--include=*.go --include=*.ts --include=*.tsx` retourne **zéro** occurrence de ces quatre variables : elles n'apparaissent que dans `.env.example`, `docker-compose.yml`, une ligne d'`echo` du `Makefile` et le tableau d'environnement du `README.md`. Le seul chemin LLM réellement implémenté est `HERMES_URL` + `HERMES_API_KEY`.

**Décision** : ce travail n'implémente pas ACP. Il retire la promesse.

- pi est piloté par son propre protocole JSONL, décrit par [docs/rpc.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md).
- À l'implémentation, les quatre variables mortes sont supprimées de `docker-compose.yml`, de `.env.example` et du tableau d'environnement du `README.md`, et remplacées par les variables `PI_*` et `BIFROST_*` de la section 9.4.

### 2.1 Dette assumée : `/ws/acp` est un nom trompeur

Ce qui circule sur `/ws/acp` n'est pas de l'ACP : c'est l'enveloppe maison `StreamEvent`. La route garde son nom **uniquement** pour la compatibilité (EX-5 : un navigateur avec un bundle en cache appelle ce chemin en dur, `Chat.tsx` ligne 237 et `LyaChat.tsx` ligne 111). C'est une dette de nommage, enregistrée comme telle. Le jour où il n'existe plus de bundle en cache dans la nature, elle pourra être renommée en `/ws/agent/lya` avec une redirection temporaire ; ce n'est pas urgent et ce n'est pas dans le périmètre.

### 2.2 Nettoyage connexe : la cible `mock-agent` du `Makefile`

Le `Makefile` contient une cible qui compile `./internal/bridge/mockagent`, package qui **n'existe pas** : `make mock-agent` échoue aujourd'hui. Le travail de test de pi a besoin d'un faux binaire pi (section 10, étape 1) : c'est l'occasion de créer réellement ce package et de rendre la cible fonctionnelle, ou de la supprimer. Les deux sont acceptables ; laisser une cible cassée ne l'est pas.

---

## 3. 🧩 Décision 3 : où vit le second agent

### 3.1 Décision

Un seul fichier nouveau dans le package existant :

```
backend/internal/bridge/
├── hermes.go        (inchangé)
├── hermes_test.go   (inchangé : c'est le garde-fou de non-régression)
├── pi.go            (nouveau)
└── pi_test.go       (nouveau)
```

`pi.go` définit **(nouveau)** `type PiBridge struct` avec `HandleWebSocket(w, r)`, qui accepte **la même** forme de message entrant (`{type: prompt|subscribe, content, system, jobId, after}`) et émet **le même** `StreamEvent`. Ce qui change dans `startJob` : au lieu d'un `POST /v1/chat/completions`, on lance un sous-processus et on traduit ses événements. Tout le reste (`Job`, `append` avec `Seq` incrémental, `Subscribe(after)`, le fan-out non bloquant vers les abonnés, `Hub.gc()`, `jobTTL`, `jobRunTimeout`, `keyFingerprint`) est réutilisé **verbatim**, sans déplacement ni renommage.

Conséquence directe et voulue : les trois tests de `hermes_test.go` ne sont pas touchés, donc ils prouvent la non-régression (EX-4).

### 3.2 L'alternative pesée puis différée

Extraire la machinerie dans **(nouveau)** `backend/internal/agent` derrière une interface du genre :

```go
type Agent interface {
    ID() string
    Label() string
    Run(ctx context.Context, prompt, system string, emit func(StreamEvent)) error
}
```

avec un registre `map[string]Agent` et un unique handler WebSocket paramétré par l'identifiant d'agent. C'est la bonne forme **le jour où un troisième agent arrive**. Aujourd'hui, cela signifierait refactorer le code le plus délicat du dépôt (le journal append-only à `Seq` monotone et le `Subscribe` « backlog puis live », qui portent la promesse de survie à la coupure de 100 s) pour deux implémentations seulement.

**Déclencheur de l'extraction**, à écrire maintenant pour ne pas avoir à en rediscuter : un troisième agent, ou la fin du pilote si les deux agents sont conservés, ou le premier besoin de faire varier le comportement du job selon l'agent (par exemple une durée maximale différente).

### 3.3 Alternatives rejetées

| Alternative | Pourquoi non |
|-------------|--------------|
| Un seul agent, choisi au démarrage par variable d'environnement | Tue le sélecteur, donc tue l'objet même du travail : « voir ce que le pilote va le plus utiliser » |
| Un `if/else` sur l'agent à l'intérieur de `HermesBridge` | Mélange deux transports (HTTP SSE et tube stdio) dans un type dont le nom annonce le premier ; rend les tests existants difficiles à garder intacts |

---

## 4. 🛣️ Décision 4 : routes et découverte des agents

### 4.1 Routes

| Route | Agent | Enregistrement |
|-------|-------|----------------|
| `/ws/acp` | Hermes/Lya | Existant. Motif nu, sans préfixe de méthode. Enregistré si `HERMES_API_KEY` est non vide |
| **(nouveau)** `/ws/agent/pi` | pi + Bifrost | Motif nu, même forme. Enregistré si pi est configuré |
| **(nouveau)** `GET /api/agents` | métadonnées | Toujours enregistré |

Le `ServeMux` de Go 1.22 et suivants accepte sans conflit un motif nu `/ws/agent/pi` à côté de `/ws/acp` et des motifs à méthode comme `GET /api/files`. `main.go` enregistre déjà `/ws/acp` sous condition et journalise un avertissement quand la clé est absente (`WARNING: No HERMES_API_KEY set`) : l'enregistrement conditionnel de la route pi suit exactement le même idiome, avec un avertissement symétrique.

### 4.2 `GET /api/agents`

```json
[
  { "id": "lya", "label": "Lya", "default": true },
  { "id": "pi",  "label": "Rédacteur", "default": false }
]
```

Règles :

- La liste ne contient que les agents **réellement** configurés côté serveur. C'est la seule manière de satisfaire EX-3 durablement, puisqu'un bundle en cache ignore par définition ce que le serveur sait.
- Si la liste a un seul élément, le frontend **ne rend pas** le bouton de mode correspondant à l'agent absent, et ramène à `desk` un mode mémorisé devenu invalide (7.3).
- Si l'appel échoue (par exemple ancien serveur, nouveau bundle), le frontend retombe sur `lya` et `/ws/acp` : c'est le comportement actuel, donc un échec sûr.
- Les `label` renvoyés servent de repli ; les libellés affichés viennent des clés i18n (section 7.2), parce que l'interface doit rester traduisible.

Une constante compilée dans le bundle a été écartée : elle serait fausse dès qu'une configuration de déploiement diffère du build, et rendrait EX-3 invérifiable.

### 4.3 Les deux chemins côté à côté

```
                    ┌──────────────────────────────────────────┐
                    │  Navigateur (React + Vite)               │
                    │                                          │
                    │  Toolbar.tsx : 3 modes desk/pi/lya       │
                    │       │                                  │
                    │       ▼                                  │
                    │  App.tsx  agent = 'lya' | 'pi'           │
                    │       │   (localStorage)                 │
                    │       ▼                                  │
                    │  Chat.tsx  wsPath = f(agent)             │
                    └───────┬──────────────────────┬───────────┘
                            │                      │
                 wss /ws/acp│                      │wss /ws/agent/pi
                            ▼                      ▼
        ┌───────────────────────────┐  ┌────────────────────────────┐
        │ HermesBridge (hermes.go)  │  │ PiBridge (pi.go, nouveau)  │
        │ ─ Job + Hub + StreamEvent │  │ ─ MÊMES Job/Hub/StreamEvent│
        │ ─ POST /v1/chat/...       │  │ ─ exec.CommandContext      │
        └───────────┬───────────────┘  └────────────┬───────────────┘
                    │ HTTPS SSE                     │ JSONL stdin/stdout
                    ▼                               ▼
        ┌───────────────────────────┐  ┌────────────────────────────┐
        │ Hermes / Lya (hors du     │  │ pi --mode rpc              │
        │ cluster applicatif)       │  │ cwd = WORKSPACE_DIR        │
        │ mémoire + skills          │  │ outils read/write/edit/... │
        │ aucun accès fichier       │  └────────────┬───────────────┘
        └───────────────────────────┘               │ HTTP (OpenAI-compatible)
                                                    ▼
                                       ┌────────────────────────────┐
                                       │ Bifrost (passerelle LLM)   │
                                       │ sortie unique + budget     │
                                       └────────────────────────────┘
```

Les deux ponts écrivent le même `StreamEvent` sur le même contrat de fil : le frontend n'a donc **qu'un seul** moteur de rendu.

---

## 5. ⚙️ Décision 5 : comment pi est lancé et ce qu'on lui donne

### 5.1 Forme de l'invocation

```go
cmd := exec.CommandContext(ctx, piBin,
    "--mode", "rpc",
    "--session-dir", filepath.Join(workDir, ".pi-sessions"),
    "--tools", piTools,              // "read,write,edit,grep,find,ls" : bash exclu
    "--no-approve",                  // ne charge aucune ressource de projet (voir 8.3)
    "--provider", "bifrost",
    "--model", piModel,
)
cmd.Dir = workDir                    // WORKSPACE_DIR, jamais autre chose
cmd.Env = scrubbedEnv()              // voir 5.2
```

Points fixes :

- `cmd.Dir` est **toujours** `WORKSPACE_DIR`. C'est la racine de tout le raisonnement de confinement de la section 8.
- La liste d'outils est une variable de configuration, pas une constante compilée, pour pouvoir la restreindre encore plus en production sans reconstruire l'image. Elle est journalisée au démarrage.
- Le répertoire de sessions commence par un point : `buildTree()` (`backend/internal/api/files.go`) ignore déjà toute entrée dont le nom commence par `.`, donc `.pi-sessions` est **invisible** dans l'arborescence de gauche sans code supplémentaire, comme l'est déjà `.programmes`.

### 5.2 Environnement du sous-processus

L'environnement est **construit**, pas hérité : on ne passe pas `os.Environ()`, sinon `HERMES_API_KEY` se retrouverait dans l'environnement de l'agent.

| Variable | Valeur | Rôle |
|----------|--------|------|
| `PI_CODING_AGENT_DIR` | `/data/pi` | Répertoire de configuration de pi (défaut `~/.pi/agent`). C'est là que le backend écrit `models.json` et `settings.json` |
| `HOME` | `/data/pi` | Évite que pi retombe sur un `~` inexistant ou non inscriptible |
| `PI_OFFLINE` | `1` | Coupe les opérations réseau de démarrage : vérification de mise à jour, mise à jour de paquets, télémétrie d'installation |
| `PI_SKIP_VERSION_CHECK` | `1` | Coupe la requête de dernière version vers `pi.dev` |
| `PI_TELEMETRY` | `0` | Coupe la télémétrie d'installation et les en-têtes d'attribution de fournisseur |
| `PATH` | minimal | Le strict nécessaire pour trouver le binaire `pi` et `node` |
| `HTTP_PROXY` / `HTTPS_PROXY` | non définies | Aucun proxy sortant : le seul égress modèle est Bifrost |

pi ajoute de lui-même `AI_AGENT=pi` et `PI_CODING_AGENT=true` dans l'environnement de ses enfants ; c'est sans conséquence ici puisque `bash` est exclu.

#### ⚠️ Collision de noms à ne pas rater

pi injecte `PI_SESSION_ID`, `PI_SESSION_FILE`, `PI_PROVIDER`, `PI_MODEL` et `PI_REASONING_LEVEL` dans l'environnement de son outil `bash`. **La variable applicative ne doit donc pas s'appeler `PI_MODEL`** : elle s'appelle `PI_MODEL_ID`. Même si `bash` est exclu du MVP, se réserver un nom déjà utilisé par pi est une bombe à retardement (il suffit qu'un jour `bash` soit réactivé pour un diagnostic).

### 5.3 Durée de vie du processus

| Règle | Détail |
|-------|--------|
| Le processus appartient au `Job`, pas à la connexion WebSocket | Une coupure WebSocket **ne tue pas** pi : c'est la promesse détachée existante, et c'est ce qui permet à une réponse longue de survivre au délai d'expiration de 100 s du reverse proxy (NF-2) |
| Seules deux choses terminent le processus | La fin normale de la réponse, ou l'expiration de `jobRunTimeout` (10 minutes) via l'annulation du `context` porté par `exec.CommandContext` |
| Aucun orphelin | Le processus est lancé dans son propre groupe de processus (`SysProcAttr.Setpgid`) et l'annulation tue le groupe, pas seulement le père ; `cmd.WaitDelay` borne l'attente après annulation pour ne pas laisser une goroutine bloquée sur `Wait()`. Comme `bash` est exclu, pi n'a en principe aucun enfant : la mesure est une ceinture de sécurité, pas le mécanisme principal |
| Fin de vie du `Job` | Inchangée : `Hub.gc()` supprime les jobs terminés après `jobTTL` (15 min) |

### 5.4 Granularité MVP : un processus par demande

**Décision MVP** : un processus pi par demande, avec `--session-dir` mais **sans** `-c`/`--continue`. Chaque demande produit donc sa propre session persistée dans `.pi-sessions`, exploitable pour le diagnostic (`--export` produit une transcription HTML) mais jamais reprise.

Pourquoi : la durée de vie du processus coïncide exactement avec celle du `Job`, déjà bornée par `jobRunTimeout`. Il n'y a ni pool à gérer, ni état à réconcilier après un redémarrage du backend, ni fuite possible. Le coût est un démarrage de processus Node par demande, négligeable face à la latence d'un appel LLM.

**v2** : un processus long-lived par session de travail, avec `--session <id>` et `-c`, ce qui donnerait un vrai contexte de conversation à pi. Déclencheur : le pilote demande explicitement que l'agent « se souvienne » de l'échange précédent, ou la mesure montre que le démarrage de processus pèse.

---

## 6. 🔌 Décision 6 : câblage Bifrost et clé d'API

### 6.1 Bifrost comme provider custom

Bifrost est une passerelle compatible OpenAI (elle est déjà citée comme telle dans `docs/etude-faisabilite-ide-web/architecture-v2.md` et c'était déjà la valeur par défaut de `OPENCODE_BASE_URL`). pi sait ajouter un tel fournisseur en configuration pure, via `models.json` dans son répertoire de configuration (relocalisé par `PI_CODING_AGENT_DIR`) :

```json
{
  "providers": {
    "bifrost": {
      "baseUrl": "http://bifrost:8080/v1",
      "api": "openai-completions",
      "apiKey": "cle-rendue-par-le-backend",
      "authHeader": true,
      "models": [
        {
          "id": "<a-decider-voir-questions-ouvertes>",
          "name": "Bifrost (redacteur de cours)",
          "reasoning": false,
          "input": ["text"],
          "contextWindow": 128000,
          "maxTokens": 16384,
          "cost": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 }
        }
      ]
    }
  }
}
```

`authHeader: true` demande à pi d'ajouter `Authorization: Bearer <clé>`. `api: "openai-completions"` est la valeur documentée comme la plus compatible.

### 6.2 ⚠️ Le piège du `\n` final sur la clé

**Décision : le backend Go rend `models.json` au démarrage**, en y écrivant la clé passée par `strings.TrimSpace`, plutôt que de laisser pi interpoler `"$BIFROST_API_KEY"`.

Le raisonnement, qui est déjà celui du dépôt :

1. Les secrets livrés depuis Infisical vers Kubernetes portent régulièrement un `\n` final.
2. `main.go` enveloppe déjà `HERMES_API_KEY` dans `strings.TrimSpace` au moment de l'analyse des drapeaux, et `NewHermesBridge` refait un `TrimSpace` en journalisant `hermes: API key had %d surrounding whitespace byte(s)`. Le commentaire dans le code dit exactement pourquoi : ce genre de secret porte un retour à la ligne.
3. Une clé non nettoyée produit un en-tête `Authorization: Bearer sk-xxx\n` que la passerelle rejette avec un message trompeur, du type « clé invalide », qui envoie le diagnostic dans la mauvaise direction pendant des heures.
4. L'interpolation `"$VAR"` de `models.json` **ne fait aucun `trim`** : le `\n` irait donc directement dans l'en-tête.

Donc : `BIFROST_API_KEY` est lue par le Go, passée par `strings.TrimSpace`, et écrite dans `models.json` (fichier créé en `0600` dans `/data/pi`, jamais dans le workspace).

**Repli entièrement déclaratif**, si l'on veut absolument éviter d'écrire le secret dans un fichier : la forme `"!command"` de `models.json`, qui exécute la commande et prend sa sortie standard, par exemple `"apiKey": "!tr -d '\\r\\n' < /run/secrets/bifrost-key"`. Attention : la documentation précise que pour `models.json`, les commandes shell sont résolues **à chaque requête**, sans TTL, sans réutilisation en cas d'échec transitoire et sans logique de récupération. C'est donc un repli, pas le choix principal : un `cat` par requête est acceptable, mais toute commande plus lourde ne le serait pas.

### 6.3 Diagnostic : réutiliser l'empreinte de clé existante

`keyFingerprint(key)` (`backend/internal/bridge/hermes.go`) renvoie les 8 premiers caractères hexadécimaux de `SHA-256(key)`, et son commentaire de documentation donne même la commande `kubectl` permettant de comparer avec le secret du cluster. C'est un excellent outil de diagnostic et il doit être réutilisé tel quel :

| Moment | Ligne journalisée | Contenu |
|--------|-------------------|---------|
| Démarrage | `pi: bridge configured (url=..., keyLen=..., keyFp=...)` | URL de Bifrost, longueur de la clé, empreinte 8 hexadécimaux |
| Nettoyage effectué | `pi: API key had N surrounding whitespace byte(s) - trimmed` | Symétrique de la ligne Hermes existante ; c'est le signal que le `\n` était bien là |
| Échec 401/403 | `pi: auth rejected (HTTP ...) with keyLen=... keyFp=... url=...` | Permet de dire en une seconde si les deux côtés portent le même secret |

Jamais la clé (NF-3). Jamais un fragment de la clé : une empreinte non réversible.

### 6.4 Drapeaux `compat` : à sonder, pas à supposer

`models.json` accepte des drapeaux de compatibilité au niveau du fournisseur ou du modèle. Bifrost étant une passerelle qui relaie vers plusieurs fournisseurs en amont, son comportement exact doit être **sondé contre le déploiement réel** avant d'écrire une valeur. Les candidats et le symptôme que chacun corrige :

| Drapeau | Symptôme qu'il corrige |
|---------|------------------------|
| `supportsDeveloperRole: false` | Le serveur ne comprend pas le rôle `developer` : le prompt système est ignoré ou provoque une erreur. pi envoie alors un rôle `system` |
| `supportsReasoningEffort: false` | La passerelle rejette le paramètre `reasoning_effort` |
| `supportsUsageInStreaming: false` | La passerelle refuse `stream_options: { include_usage: true }`, ou renvoie une erreur en début de flux |
| `supportsFinishReason: false` | Les réponses en flux n'incluent pas `finish_reason` : pi doit déduire la fin de lui-même |
| `maxTokensField: "max_tokens"` | La passerelle attend `max_tokens` et non `max_completion_tokens` |

Méthode de sondage, à faire pendant l'implémentation et pas avant : un seul échange court, tous les drapeaux non renseignés, et on n'ajoute une valeur que si un symptôme apparaît. Renseigner un drapeau « par précaution » masque le vrai comportement de la passerelle.

### 6.5 Bifrost reste le point de sortie unique

| Règle | Conséquence |
|-------|-------------|
| Aucune clé Anthropic ou OpenAI dans le conteneur | pi n'a matériellement pas d'autre chemin que Bifrost. C'est aussi pourquoi les variables mortes `ANTHROPIC_API_KEY` et `OPENAI_API_KEY` sont supprimées et non recyclées (EX-11) |
| Un seul fournisseur déclaré dans `models.json` | Pas de repli silencieux vers un fournisseur direct en cas de panne de Bifrost : l'échange échoue proprement avec `Échec IA. Réessaie.` |
| Budget et observabilité | Restent là où ils sont déjà : côté Bifrost, dont le tableau de bord est déjà relié depuis `Toolbar.tsx` |

---

## 7. 🔀 Décision 7 : les trois modes et la comparaison du pilote

C'est la pièce centrale du dispositif. Les trois façons de travailler restent disponibles **en même temps**, le pilote passe de l'une à l'autre, et c'est l'usage qui révèle la gagnante. Tout le reste du document existe pour rendre cette section possible.

> **Révision** : cette section a été refondue après décision de faire de pi un **troisième mode de premier niveau** (`desk` / `pi` / `lya`) plutôt qu'un sélecteur d'agent imbriqué dans le mode Desk. Les sections 7.1 à 7.4 sont réécrites, 7.5 et 7.7 ajustées ; les sections 1 à 6 et 8 à 10 ne sont pas affectées, la couture `agent` étant conservée et simplement dérivée du mode.

### 7.1 Où vit le contrôle : un troisième mode, pas un second sélecteur

**Décision** : pi devient un **mode de premier niveau**, à côté des deux qui existent déjà.

```ts
// Toolbar.tsx — avant
export type AppMode = 'desk' | 'lya'
// après
export type AppMode = 'desk' | 'pi' | 'lya'
```

Ce que sont les trois modes, en une ligne chacun — les deux premiers existent, seul le deuxième est neuf :

| Mode | Écran | Agent | Prompt système | Fichiers |
|------|-------|-------|----------------|----------|
| `desk` | 3 panneaux : arborescence, éditeur, chat | Hermes/Lya via `/ws/acp` | `buildSystemPrompt(programme)` | L'enseignante écrit ; « 📝 Insérer » pour reprendre une réponse |
| **`pi`** | **Les mêmes 3 panneaux** | **pi via `/ws/agent/pi`** | **Le même `buildSystemPrompt(programme)`** | **pi lit et écrit le fichier lui-même** |
| `lya` | Plein écran conversationnel | Hermes/Lya via `/ws/acp` | **aucun** | aucun |

#### Pourquoi trois modes plutôt qu'un sélecteur d'agent imbriqué

La version précédente de ce document ajoutait un second contrôle (`AgentId`) *à l'intérieur* du mode Desk. Trois raisons de préférer le mode de premier niveau :

1. **La combinaison invalide devient irreprésentable.** Le modèle imbriqué est une matrice 2×2 dont la case Lya × pi n'a pas de sens — la section 7.4 devait l'*expliquer*. Avec trois modes, il n'y a rien à expliquer : l'état n'existe pas. C'est un meilleur automate, pas seulement une meilleure interface.
2. **Un seul contrôle à apprendre, et il ne bouge pas.** Le modèle imbriqué fait apparaître et disparaître un sélecteur selon le mode courant. Pour une utilisatrice non développeuse, une rangée de trois boutons toujours au même endroit est strictement plus simple qu'un contrôle conditionnel.
3. **C'est cohérent avec la thèse de la section 7b.** Si Hermes et pi sont bien *deux produits différents et pas deux versions du même*, alors ils appartiennent au niveau où l'application distingue déjà ses produits : le sélecteur de mode. Les enfouir sous un mode revenait à traiter comme un réglage ce que le document affirme être une différence de nature.

#### Ce que cela supprime

Le modèle à trois modes est **moins de code** que celui qu'il remplace. Disparaissent purement et simplement :

| Artefact prévu par la version précédente | Devient |
|---|---|
| `export type AgentId = 'lya' \| 'pi'` | supprimé — `AppMode` suffit |
| `div.toolbar-agent-switcher`, `button.toolbar-agent-btn` | supprimés — `toolbar-mode-switcher` / `toolbar-mode-btn` réutilisés tels quels |
| `AGENT_STORAGE_KEY`, `loadAgent()`, `saveAgent()`, `handleAgentChange` | supprimés — `MODE_STORAGE_KEY`, `loadMode()`, `saveMode()`, `handleModeChange` étendus d'une valeur |
| Groupe i18n `agent` (7 clés) | réduit à 2 clés dans le groupe `mode` existant + 3 clés d'indication |
| Rendu conditionnel du sélecteur selon le mode | supprimé — le sélecteur est toujours celui du mode |

Aucun cadriciel CSS n'est introduit (règle de `AGENTS.md`) : le balisage du sélecteur ne change pas, il gagne un bouton.

#### ⚠️ Le piège d'implémentation : ne pas tripler le layout

`App.tsx` porte **deux** ternaires `mode === 'desk' ? … : …` — un dans la branche mobile (`MobileLayout` contre `LyaChat`) et un dans la branche bureau (le `div.workspace` avec `Allotment` contre `LyaChat`). Ajouter naïvement une troisième branche dupliquerait tout le layout 3 panneaux, `Allotment` compris.

**Le mode pi partage le layout du mode desk ; seul l'agent du panneau de chat change.** La transformation correcte est donc, dans les deux branches :

```ts
// App.tsx — dérivations à introduire
const isWorkspace = mode === 'desk' || mode === 'pi'
const chatAgent: 'lya' | 'pi' = mode === 'pi' ? 'pi' : 'lya'
```

```ts
// les deux ternaires : mode === 'desk'  →  isWorkspace
{isWorkspace ? ( /* layout inchangé */ ) : ( <LyaChat userName={userName} /> )}
```

et une seule propriété nouvelle transmise au chat : `<Chat agent={chatAgent} … />`, `MobileLayout` la relayant à l'identique.

**La couture `agent` de la version précédente survit donc intacte** — elle est simplement *dérivée du mode* au lieu d'être un second contrôle. C'est ce qui fait que le reste de ce document reste valable sans retouche : routes (D4), protocole RPC (D6), câblage Bifrost (D7), frontière fichiers (D10, D13), emballage (D12) sont inchangés. Seules les sections 7.1 à 7.4 sont révisées.

De la même façon, les contrôles propres au poste de travail dans `Toolbar.tsx` (niveau, programme, fichier courant, exports) sont aujourd'hui sous `{mode === 'desk' && …}` : ils passent sous la même condition `isWorkspace`, puisqu'en mode pi l'enseignante a toujours besoin de choisir son niveau et d'exporter son cours.

#### 🐞 Constat annexe pendant cette révision

Dans la branche bureau, `LyaChat` est appelé **sans** `userName` (`<LyaChat />`), alors que la branche mobile passe bien `<LyaChat userName={userName} />`. Or `LyaChat` n'utilise `userName` que pour préfixer le premier message par `[Je suis …]`. Conséquence : **sur ordinateur, Lya ne sait pas à qui elle parle ; sur téléphone, oui.** Hors périmètre de cette spec, correction d'une ligne, mais à traiter — c'est exactement le genre d'asymétrie qui faussera une comparaison d'agents portant en partie sur la qualité relationnelle.

### 7.2 Libellés

Il y a ici une tension réelle à trancher, et la version précédente de ce document l'avait tranchée dans le vide : elle posait comme principe que « le pilote n'a pas à savoir ce qu'est pi », alors que **les deux modes existants sont déjà étiquetés par des noms techniques** — `mode.desk` vaut littéralement `Desk` et `mode.lya` vaut `Lya`, dans `fr.json` comme dans `en.json`. Ajouter `✍️ Rédiger` à côté de `Desk` et `Lya` produirait une rangée incohérente.

**Décision** : on garde le registre existant, on n'y touche pas, et le troisième bouton s'appelle `Pi`. Deux raisons : ne pas renommer sous les pieds du pilote deux modes qu'elle utilise déjà, et rester dans le vocabulaire employé par le commanditaire (« mode pi », « mode desk », « mode Lya »).

Le sens ne passe donc pas par le libellé du bouton mais par deux porteurs, ajoutés **dans les deux** fichiers `frontend/src/i18n/fr.json` et `frontend/src/i18n/en.json` :

| Clé | fr.json | en.json |
|-----|---------|---------|
| `mode.pi` | `Pi` | `Pi` |
| `mode.piTitle` *(infobulle du bouton)* | `Pi — modifie directement tes fichiers de cours` | `Pi — edits your course files directly` |
| `mode.deskTitle` *(infobulle, ajoutée par symétrie)* | `Desk — Lya répond dans le chat, tu insères ce que tu gardes` | `Desk — Lya answers in the chat, you insert what you keep` |
| `chat.piHint` *(bandeau du panneau de chat en mode pi)* | `Pi modifie directement le fichier ouvert.` | `Pi edits the open file directly.` |
| `chat.piWorking` | `✍️ Modification du fichier en cours...` | `✍️ Editing the file...` |
| `chat.piUpdated` | `Fichier mis à jour par Pi.` | `File updated by Pi.` |

Les trois dernières clés vont dans le groupe `chat` existant, pas dans un groupe `agent` nouveau : elles décrivent l'état du panneau de chat, qui est là où l'enseignante les lira.

**Variante si tu préfères la lisibilité à la continuité** : renommer les trois d'un coup en libellés d'usage — `📚 Mes cours` / `✍️ Rédaction` / `💬 Lya`. C'est plus parlant pour une enseignante, mais ça change deux libellés qu'elle connaît déjà, et ça n'est pas ton vocabulaire. À toi de choisir ; le reste du design est identique dans les deux cas.

### 7.3 Persistance

**Aucune clé de stockage nouvelle.** Le mode est déjà persisté ; il gagne une valeur admissible. Dans `App.tsx`, une seule ligne change :

```ts
// avant
if (saved === 'desk' || saved === 'lya') return saved
// après
if (saved === 'desk' || saved === 'pi' || saved === 'lya') return saved
```

`MODE_STORAGE_KEY`, `saveMode()`, `handleModeChange` et le repli sur `'desk'` pour toute valeur inconnue sont inchangés. Le repli couvre gratuitement deux cas : un bundle plus ancien qui ne connaît pas `'pi'`, et le cas où pi n'est pas configuré côté serveur (voir ci-dessous).

**Croisement avec `GET /api/agents` (D5)** : si le point d'entrée n'annonce pas pi, le bouton `Pi` n'est pas rendu, et un `'pi'` déjà présent en `localStorage` est ramené à `'desk'` au chargement. C'est ce qui rend EX-13 — retour arrière trivial — vrai sans intervention : on retire la variable d'environnement, le mode disparaît de l'interface et les navigateurs qui l'avaient mémorisé retombent sur Desk.

Le mode est ensuite dérivé en agent (`chatAgent`, voir 7.1) et passé en propriété à `Chat`, et `Chat.tsx` n'a besoin **que d'une ligne changée** : le chemin en dur de la ligne 237 devient un chemin dérivé de l'agent.

```ts
// avant (ligne 237)
const wsUrl = `${protocol}//${window.location.host}/ws/acp`

// après
const wsPath = agent === 'pi' ? '/ws/agent/pi' : '/ws/acp'
const wsUrl = `${protocol}//${window.location.host}${wsPath}`
```

Tout le reste est inchangé : les options `AuthWebSocket` (`url`, `onMessage`, `onOpen`, `onDisconnect`, `onReconnect`, `maxRetries: 5`, `baseDelay: 1000`), le `subscribe` de reconnexion, la gestion du `jobId`. `LyaChat.tsx` n'est **pas** touché.

### 7.4 Ce que trois modes changent pour la mesure

Le modèle imbriqué mesurait la préférence entre deux agents *à l'intérieur* du mode Desk, et la section correspondante devait prévenir que l'effondrement éventuel du mode Lya serait un « signal indirect » à ne pas confondre avec la comparaison principale. Cette réserve tombe : **les trois façons de travailler sont désormais sur la même échelle, lisible dans une seule répartition.**

Ce que le pilote mesure exactement, et c'est plus riche qu'avant :

| Comparaison | Ce qu'elle dit |
|---|---|
| `desk` contre `pi` | À agent pédagogique et écran identiques, l'enseignante préfère-t-elle **qu'on lui propose du texte** ou **qu'on modifie son fichier** ? C'est la comparaison principale, et elle est propre : même layout, même `buildSystemPrompt`, même niveau, seul l'agent diffère |
| `desk` + `pi` contre `lya` | Le compagnon plein écran garde-t-il sa place quand deux modes de travail existent ? |
| `pi` contre `lya` | Les deux extrêmes de l'échelle d'autonomie : celui qui agit sur les fichiers contre celui qui se souvient et ne touche à rien |

**Les deux modes Hermes restent distinguables** dans la mesure, ce qui compte pour répondre à la question du pilote : `desk` et `lya` appellent tous deux `/ws/acp`, mais la ligne `agent_usage` ne suffit alors plus à les séparer. D'où l'ajustement de la section 7.7 : la ligne de journal porte `agent=lya` **et** `mode=desk|lya`. Sans ce champ, « Hermes est-il overkill ? » resterait sans réponse, puisqu'on ne saurait pas si Hermes est sollicité comme outil de rédaction ou comme compagnon — or c'est précisément la distinction que la section 7b appelle « mal apparié plutôt que surdimensionné ».

**Le biais de nouveauté est plus fort qu'avant** et il faut le dire : un bouton neuf dans une rangée que l'enseignante regarde à chaque session attire mécaniquement plus qu'un sélecteur imbriqué. La lecture des chiffres doit donc écarter la première semaine, comme le prévoit déjà la condition de sortie des exigences.

### 7.5 Que se passe-t-il à la bascule

| Question | Décision | Motif |
|----------|----------|-------|
| Un job en cours est-il annulé ? | **Non, jamais.** Il va jusqu'à son terme ou jusqu'à `jobRunTimeout` | Deux raisons : tuer un pi au milieu d'une écriture peut laisser un fichier tronqué, et la promesse détachée du `Job` interdit qu'un événement d'interface tue un traitement |
| Peut-on alors basculer pendant un traitement ? | **Oui, la bascule reste permise** — révision liée au passage à trois modes. Le job continue, et l'onglet quitté le retrouve au retour grâce à `Subscribe(after)` | Dans le modèle imbriqué, le contrôle ne changeait que l'agent : le désactiver était sans conséquence. Devenu sélecteur de **mode**, il change l'écran entier : interdire de retourner voir ses fichiers ou de poser une question à Lya parce qu'une génération est en cours serait une régression d'usage. Le journal append-only et le rejeu du backlog existent exactement pour ça |
| Que voit-on en revenant sur un mode dont le job tourne encore ? | Le fil se reconstitue depuis le backlog, streaming compris s'il n'est pas terminé | C'est déjà le comportement du `subscribe` de reconnexion dans `Chat.tsx` : aucun code nouveau, seulement un cas d'usage supplémentaire pour un mécanisme éprouvé |
| Et le verrou de lecture seule sur le fichier pendant que pi écrit (7.6) ? | Il **tient à travers la bascule** : quitter le mode pi ne déverrouille pas le fichier que pi est en train d'écrire | Sinon la bascule offrirait précisément le moyen de contourner la protection contre les deux auteurs simultanés |
| L'historique est-il partagé ? | **Non.** Le fil affiché reste à l'écran pour l'enseignant, mais rien n'est rejoué vers l'agent nouvellement sélectionné | Le protocole est déjà sans historique aujourd'hui : `Chat.tsx` envoie `{type:'prompt', content, system}` et rien de plus. Introduire un historique partagé serait une fonctionnalité nouvelle, et elle polluerait la comparaison en donnant à un agent le contexte produit par l'autre |
| Comment l'utilisateur voit-il la bascule ? | Le bouton de mode actif porte déjà la classe `active` ; en mode pi le panneau de chat affiche en plus le bandeau `chat.piHint` | Un non-développeur doit pouvoir répondre à « qui m'a répondu ça ? » en regardant l'écran. Avec trois modes c'est acquis : le mode courant est toujours visible dans la barre d'outils, alors qu'un sélecteur imbriqué pouvait être hors du champ de vision |

### 7.6 Après une écriture de pi : ce que l'interface fait

Proposer « 📝 Insérer dans le cours » (`chat.insert`) après que l'agent a lui-même écrit dans le fichier dupliquerait le contenu. Donc, sur un `done` provenant de l'agent pi dont le flux contenait au moins un appel d'outil d'écriture :

1. Le bouton d'insertion n'est pas rendu pour ce message ; à sa place, le texte `chat.piUpdated`.
2. Le contenu du fichier courant est relu depuis le serveur (`getText` de `api.ts` sur `GET /api/file`) et poussé dans l'éditeur.
3. L'arborescence est rafraîchie en incrémentant `refreshKey` (le mécanisme existe déjà : `App.tsx` porte `refreshKey` et `handleFileTreeRefresh`, `FileTree` refait sa requête quand la valeur change, ce qui est même déjà couvert par un test).

Cela demande deux propriétés optionnelles nouvelles sur `Chat` : **(nouveau)** `onFileChanged` et **(nouveau)** `onRefreshTree`, câblées dans `App.tsx` sur des fonctions qui existent déjà.

**Concurrence avec l'autosauvegarde** : l'éditeur enregistre tout seul via `useAutoSave`, pendant que l'agent peut écrire le même fichier. Il n'y a pas de verrou de fichier et il n'en faut pas ; il faut une règle claire :

| Règle | Mise en oeuvre |
|-------|----------------|
| Avant d'envoyer une demande à pi, les modifications locales en attente sont écrites | `App.tsx` porte déjà `flushRef` et le passe à l'éditeur par `onFlushRef` ; on l'appelle avant l'envoi, comme le fait déjà l'export |
| Pendant le job pi, l'éditeur n'écrase pas le travail de l'agent | Le fichier visé passe en lecture seule avec l'indication `chat.piWorking`. C'est aussi honnête pour l'utilisateur : deux auteurs simultanés sur un même fichier, c'est un piège, pas une fonctionnalité |
| Après le job, le disque gagne | Relecture serveur (point 2 ci-dessus) : le contenu à l'écran redevient celui du fichier |
| Si l'utilisateur avait quand même modifié le texte pendant le job | Cas impossible tant que la lecture seule tient. S'il devient possible (par exemple sur un autre onglet), le disque gagne toujours, et c'est documenté ici comme un choix, pas comme un accident |

### 7.7 Mesurer : une ligne par demande

Une seule ligne de journal par demande, produite par `log.Printf` comme le reste du backend, donc dans le même flux que les lignes `hermes:` et récupérable par les mêmes moyens (`kubectl logs`, `grep`) :

```
agent_usage agent=pi mode=pi jobId=job-1756988112345678901 promptLen=412 file=B1/unit5.md durationMs=8421 status=done tools=3 toolsUsed=read,edit
```

| Champ | Présent pour | Sert à |
|-------|--------------|--------|
| `agent` | les trois | Quel harnais a répondu : `lya` ou `pi` |
| `mode` | les trois | **Ajouté avec le passage à trois modes.** `desk`, `pi` ou `lya`. Indispensable : `desk` et `lya` appellent tous deux Hermes, et sans ce champ on ne saurait pas si Hermes est sollicité comme outil de rédaction ou comme compagnon — c'est-à-dire qu'on ne pourrait pas répondre à la question du pilote. Le frontend le transmet dans le message `prompt` |
| `jobId` | les deux | Corréler avec les autres lignes du même échange |
| `promptLen` | les deux | Distinguer les demandes courtes des demandes élaborées, sans lire le contenu |
| `file` | les deux | Savoir sur quel type de fichier chaque agent est sollicité (chemin relatif au workspace, pas de contenu) |
| `durationMs` | les deux | Confort d'usage comparé |
| `status` | les deux | `done` ou `error` : fiabilité comparée |
| `tools`, `toolsUsed` | pi seulement | Est-ce que la capacité d'agir sert vraiment, ou est-ce que pi se contente de répondre du texte ? |

**Conséquence sur le protocole, à ne pas laisser implicite** : pour que le backend puisse journaliser `mode`, le message `prompt` le porte. La structure anonyme décodée dans `HandleWebSocket` gagne donc un champ `Mode string \`json:"mode,omitempty"\``, et `/ws/acp` le reçoit aussi. Ce n'est pas une violation d'EX-4 : un champ absent se décode en chaîne vide, traitée comme `desk`, donc un bundle en cache qui ne l'envoie pas continue de fonctionner à l'identique (EX-5). C'est le seul ajout au protocole existant de tout ce document, et il est purement d'observation : aucune décision de routage n'en dépend — le routage se fait par la route, pas par ce champ.

**Contraintes** (NF-5), non négociables : champs techniques et agrégeables uniquement. Pas une ligne de contenu de cours, pas de nom, pas d'adresse de courriel, pas d'identifiant d'utilisateur. `promptLen` est un entier, pas un extrait. `file` est un chemin relatif, ce qui est déjà public dans l'arborescence de l'interface.

**Ce que le frontend peut ajouter, et pourquoi c'est en dernier** : le signal « l'enseignant a gardé cette réponse » n'existe côté Hermes que sous la forme d'un clic sur « Insérer dans le cours », et le backend ne peut pas le deviner, puisque l'insertion se termine par la même écriture de fichier que n'importe quelle frappe au clavier. Le collecter demande un point d'entrée minimal **(nouveau)** `POST /api/usage` acceptant `{agent, action}` et rien d'autre. C'est décidé, mais placé au dernier incrément du plan de mise en oeuvre : la ligne backend seule suffit déjà à satisfaire EX-12, et il ne faut pas retarder le pilote pour un signal secondaire.

**Signal complémentaire déjà disponible** : côté Bifrost, jetons et coût du chemin pi sont visibles dans le tableau de bord déjà relié depuis `Toolbar.tsx` (`a.toolbar-dashboard-link`). Le chemin Hermes n'y apparaît pas, puisqu'il ne passe pas par Bifrost : la comparaison de coût est donc partielle par construction, et il faut le dire en lisant les chiffres plutôt que de faire semblant de comparer deux colonnes homogènes.

### 7.8 🏁 Verdict du pilote

La question doit avoir une façon définie d'être tranchée, sinon le dispositif ne sert à rien. Le passage à trois modes fait apparaître une quatrième issue, et c'est celle que la thèse de la section 7b rend la plus probable : **Hermes conservé mais relégué au compagnon**. Elle était invisible dans le modèle imbriqué, qui ne savait pas distinguer Hermes-outil de Hermes-compagnon.

| Issue | Signaux qui la justifient | Ce qu'on fait alors |
|-------|---------------------------|---------------------|
| **Garder les trois** | La répartition reste équilibrée sur les dernières semaines ; les trois modes sont sollicités sur des demandes de nature différente ; le coût de maintien des deux chemins reste faible | On procède à l'extraction du package `agent` prévue en 3.2, puisque la coexistence devient durable |
| **Hermes relégué au compagnon** ⬅ *issue rendue visible par les trois modes* | `mode=pi` domine `mode=desk` sur les demandes de rédaction, alors que `mode=lya` garde un usage régulier | Le mode `Desk` est retiré ou basculé sur pi ; Hermes reste, mais uniquement derrière le mode `Lya`. Réponse à la question : Hermes n'était pas surdimensionné, il était **mal apparié** — exactement la thèse de 7b, confirmée par l'usage |
| **Abandonner Hermes** | L'usage bascule durablement vers pi et ne revient pas ; le clic « Insérer dans le cours » devient rare ; `mode=lya` tombe aussi en désuétude, donc ni la mémoire ni les skills ne servent plus ; les incidents d'exploitation côté passerelle Hermes continuent de coûter du temps | On retire le pont Hermes, `HERMES_*`, et `/ws/acp` devient une redirection puis disparaît. Le mode `Lya` disparaît avec lui — c'est la condition qui rend cette issue distincte de la précédente, et elle doit être vérifiée avant de trancher. Réponse : oui, Hermes était surdimensionné pour cet usage |
| **Abandonner pi** | pi est peu utilisé après la phase de nouveauté ; ses écritures directes sont plus souvent défaites que gardées ; sa qualité de français ou d'anglais pédagogique est jugée inférieure ; le poids d'image et le confinement ne se justifient pas | On retire pi, l'image backend redevient ce qu'elle est aujourd'hui, et le mode `Pi` disparaît de lui-même (EX-13 et 7.3). Réponse : non, Hermes n'était pas surdimensionné |

Ce document ne préjuge d'aucune de ces quatre issues. Il garantit seulement qu'à la fin du pilote, on aura de quoi choisir.

---

## 7b. 🤔 Hermes est-il overkill ?

C'est la question posée par le pilote, et elle mérite une réponse argumentée plutôt qu'un haussement d'épaules.

### 7b.1 La thèse : pas surdimensionné, mais mal apparié

Hermes/Lya n'est pas trop gros pour la tâche : il fait **autre chose** que ce dont l'écriture de cours a besoin en aval de la réflexion.

| | Hermes / Lya | pi + Bifrost |
|---|---|---|
| Nature | Agent **conversationnel** : un appel HTTPS, mémoire et skills côté serveur | Agent **acteur** : un sous-processus local avec des outils de fichiers |
| Chemin vers le cours | Le texte arrive dans le chat ; il n'entre dans le cours que par le bouton « Insérer dans le cours » | Il modifie `B1/unit5.md` sur place |
| Surface de risque fichier | **Nulle.** Aucun accès au système de fichiers | Réelle, et c'est tout l'objet de la section 8 |

Deux produits différents, donc : trois modes, pas une migration. La bonne question n'est pas « lequel est le meilleur agent », c'est « pour écrire des cours, l'enseignant préfère-t-il un interlocuteur ou un exécutant ». Et personne ne peut y répondre à sa place.

### 7b.2 Comparaison sur les critères qui comptent pour l'écriture de cours

| Critère | Hermes / Lya | pi + Bifrost |
|---------|--------------|--------------|
| Qualité du français et de l'anglais pédagogique | Dépend du modèle derrière la passerelle Hermes, non choisi depuis l'application | Dépend du modèle choisi dans `models.json`, donc pilotable et changeable sans redéploiement de code |
| Respect des données du programme officiel | Le prompt système construit par `buildSystemPrompt()` est transmis dans le champ `system` à chaque demande | Même contenu, transmis en tour de préambule (D9) : mécanisme différent, information identique |
| Modifier un fichier `.md` | Impossible : retourne du texte à insérer | C'est sa raison d'être |
| Contrôle du prompt système | Partiel : Hermes a ses propres skills et sa propre mémoire, non visibles depuis l'application | Total : liste d'outils, prompt, modèle, tout est dans la configuration que le backend rend |
| Surface d'exploitation | Un appel HTTPS sortant. Rien à installer dans l'image | Un runtime Node et un paquet npm dans l'image, un processus enfant, un répertoire d'état inscriptible |
| Modes de défaillance | Erreur d'authentification de la passerelle, indisponibilité, latence | Processus qui meurt, JSONL malformé, boucle d'outils, épuisement du délai de 10 minutes, fichier laissé dans un état intermédiaire |
| Latence | Un aller-retour réseau vers la passerelle | Démarrage d'un processus Node par demande, puis flux via Bifrost |
| Coût | Non visible depuis Bifrost, donc non comparable directement (voir 7.7) | Visible et plafonnable dans Bifrost, qui est le point de contrôle du budget |
| Piège d'exploitation connu | La passerelle lit sa clé `API_SERVER_KEY` depuis un **fichier sur son volume persistant** et non depuis une variable d'environnement Kubernetes : les 401 ressemblent alors à une désynchronisation de secret et sont lents à diagnostiquer. C'est précisément pour cela que `keyFingerprint` existe dans ce dépôt | Le même piège est évité par construction : la clé est nettoyée par le Go et l'empreinte est journalisée au démarrage et sur 401/403 (6.3) |

### 7b.3 Ce qui tranchera

Rien dans ce tableau ne désigne un gagnant, et c'est volontaire. Deux observations trancheront, et elles ne peuvent venir que de l'usage :

1. **La fréquence du clic « Insérer dans le cours ».** S'il est constant, le circuit conversationnel suffit, et l'agent qui écrit tout seul est une complication. S'il devient pénible, c'est que l'enseignant veut un exécutant.
2. **Le sort des écritures de pi.** Si elles sont gardées, la capacité d'agir a de la valeur. Si elles sont systématiquement défaites, c'est une jolie démonstration technique sans utilité pédagogique.

Les deux se lisent dans les signaux de la section 7.7, et la décision revient au pilote (voir requirements.md, « Qui décide »).

---

## 8. 🛡️ Décision 8 : la frontière du système de fichiers

C'est la zone la plus risquée du travail, et elle est traitée comme telle : c'est la seule section dont une erreur peut détruire du contenu.

### 8.1 Ce que protège le code actuel, et ce qu'il ne protège pas

Toutes les opérations de fichiers exposées par l'API passent par une seule fonction, `safePath()` dans `backend/internal/api/files.go` :

```go
cleaned := filepath.Clean(relPath)
if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
    return "", fmt.Errorf("invalid path: %s", relPath)
}
abs := filepath.Join(h.workDir, cleaned)
if !strings.HasPrefix(abs, filepath.Clean(h.workDir)) {
    return "", fmt.Errorf("path escapes workspace: %s", relPath)
}
```

C'est correct, c'est testé (`TestReadFile_PathTraversal`, `TestWriteFile_PathTraversal`, `TestDeleteFile_PathTraversal`, `TestMkDir_PathTraversal`), et c'est **totalement hors-jeu** pour un sous-processus. Un pi qui écrit avec ses propres outils `write` ou `edit` ne passe par aucune ligne de Go de ce dépôt. Il n'existe aujourd'hui **rien** dans le code qui contraigne un sous-processus d'agent à rester dans `WORKSPACE_DIR`, pour une raison simple : il n'y a jamais eu de sous-processus d'agent.

### 8.2 Ce que pi garantit, et ce qu'il ne garantit pas

Il faut être précis, parce que le projet pi l'est :

| Fait, documenté | Conséquence ici |
|-----------------|-----------------|
| pi n'a **pas de bac à sable intégré** et tourne avec les droits du compte qui le lance ; les outils lisent, écrivent, éditent et exécutent avec ces droits | La frontière ne peut pas venir de pi. Elle doit venir du conteneur, de l'utilisateur système et de la liste d'outils |
| pi n'a **pas de fenêtre de permission** : c'est une non-fonctionnalité assumée, la réponse officielle étant « faites tourner pi dans un conteneur » | Rien à cliquer, donc rien à oublier de cliquer : tout se décide au lancement |
| Les modes non interactifs (`-p`, `--mode json`, `--mode rpc`) **n'affichent aucune invite de confiance** ; sans décision enregistrée, le comportement suit `defaultProjectTrust` du réglage global (`ask` par défaut, ou `always`, ou `never`), surchargeable par exécution avec `--approve`/`-a` ou `--no-approve`/`-na` | La confiance projet doit être **fixée explicitement**. Une valeur par défaut est un choix par omission, et ici l'omission porte sur du code exécutable |
| La confiance projet contrôle le chargement de `.pi/settings.json`, `.pi/extensions`, `.pi/skills`, `.pi/prompts`, `.pi/themes`, `.pi/SYSTEM.md`, `.pi/APPEND_SYSTEM.md` et des `.agents/skills` du répertoire courant ou d'un ancêtre | Un fichier déposé dans le workspace pourrait **remplacer le prompt système** de l'agent ou lui ajouter des extensions, si la confiance était accordée |
| Les fichiers de contexte `AGENTS.md`, `AGENTS.override.md` et `CLAUDE.md` sont chargés **indépendamment** de la confiance projet, sauf si le chargement de contexte est désactivé (`--no-context-files` / `-nc`) | Un `AGENTS.md` déposé dans le workspace serait lu par l'agent même sans confiance. Il faut donc désactiver explicitement le chargement de contexte |
| La confiance projet n'est **pas** un bac à sable : elle ne restreint pas ce que le modèle peut demander aux outils une fois la session démarrée. L'injection de prompt depuis les fichiers du dépôt est un risque assumé du modèle « agent local » | Le confinement doit être structurel (utilisateur, montages, liste d'outils), pas déclaratif |

### 8.3 La conséquence pour cette application

Le point à ne pas manquer : **le workspace est inscriptible par l'enseignant**, via `PUT /api/file`, `POST /api/files/mkdir` et `POST /api/files/rename`. Tout ce qui s'y trouve est donc, du point de vue de pi, une **entrée non fiable**. Ce n'est pas une hypothèse d'attaquant sophistiqué : il suffit qu'un fichier collé depuis le web contienne des instructions, ou qu'un fichier soit nommé `AGENTS.md` par curiosité.

D'où les décisions suivantes, toutes à appliquer ensemble :

| # | Décision | Ce que cela empêche |
|---|----------|---------------------|
| 8a | `cmd.Dir = WORKSPACE_DIR`, toujours | pi ne remonte pas dans l'arborescence pour trouver des ressources de projet ailleurs |
| 8b | `defaultProjectTrust` fixé à `never` dans le `settings.json` que le backend rend dans `/data/pi`, **et** `--no-approve` passé à chaque exécution | Aucun `.pi/settings.json`, aucune extension, aucun skill, aucun `SYSTEM.md` déposé dans le workspace n'est chargé |
| 8c | `--no-context-files` | Aucun `AGENTS.md` ni `CLAUDE.md` du workspace n'entre dans le prompt (ce chargement échappe à la confiance projet). ✅ **Vérifié expérimentalement** contre pi 0.84.2 : sans ce drapeau, un marqueur déposé dans `workspace/AGENTS.md` apparaît dans la requête envoyée au modèle. Retiré à tort en v1.8.0, rétabli en v1.8.1 |
| 8d | `bash` **exclu** de la liste d'outils : `--tools read,write,edit,grep,find,ls` | Un assistant de rédaction n'a aucun besoin de shell. Et `bash` est le seul outil qu'aucune frontière de chemin ne peut contenir : avec un shell, la liste d'outils devient décorative. **C'est la décision la plus importante de ce document** |
| 8e | Processus lancé sous un identifiant utilisateur non privilégié, propriétaire uniquement de `/workspace` et de `/data/pi` | Une écriture hors de ces deux répertoires échoue au niveau du noyau, pas au niveau d'une vérification applicative |
| 8f | `/data/programmes` monté en **lecture seule** | Les programmes officiels ne peuvent pas être altérés par l'agent, alors que la lecture reste possible |
| 8g | `/ws/agent/pi` hérite de l'authentification directe Authelia existante | C'est le même hôte derrière le même reverse proxy que `/ws/acp` : aucun travail supplémentaire, mais il faut le vérifier, pas le supposer |
| 8h | `models.json` et `settings.json` écrits dans `/data/pi`, en `0600`, jamais dans le workspace | Le secret Bifrost n'est pas visible depuis l'arborescence de fichiers de l'interface |

Alternative rejetée : inclure `bash` avec une liste blanche de commandes construite dans l'application. Rejetée parce qu'une liste blanche de commandes shell est un exercice notoirement perdant (substitutions, redirections, chaînages), pour un bénéfice nul dans ce produit.

### 8.4 Ce qui n'est pas couvert, et qui doit être dit

| Risque résiduel | Pourquoi il reste | Atténuation |
|-----------------|-------------------|-------------|
| Injection de prompt depuis un fichier de cours | Le modèle lit les fichiers : c'est sa fonction. pi lui-même documente ce risque comme non évitable | La liste d'outils réduit l'impact à des écritures dans le workspace, et l'utilisateur voit l'écriture (7.6) |
| Écriture destructrice dans le workspace | `write` et `edit` sont accordés, donc l'agent peut écraser un fichier de cours | Le workspace doit être sauvegardé. Le point de restauration est un sujet d'exploitation, pas de code, et il est signalé ici comme prérequis du pilote |
| Fuite de contenu de cours vers le modèle | Inhérent à l'usage d'un LLM | Bifrost est le point unique où cette sortie est visible et contrôlable |

Pour la concurrence entre l'autosauvegarde de l'éditeur et l'écriture de l'agent, voir 7.6 : le fichier visé passe en lecture seule pendant le job, et le disque gagne après.

---

## 9. 📦 Décision 9 : emballage et déploiement

### 9.1 Node dans l'image backend

pi est une CLI Node : `@earendil-works/pi-coding-agent@0.84.2` déclare `engines.node >= 22.19.0`. Or l'étage d'exécution de `backend/Dockerfile` est un `alpine:3.20` minimal auquel on ajoute `ca-certificates`, `pandoc` et `typst` : **il n'y a ni node ni npm**. Embarquer pi implique donc d'ajouter un runtime Node et le paquet épinglé.

Correspondance étiquette Alpine vers version majeure de Node, relevée sur l'index de paquets Alpine (`https://pkgs.alpinelinux.org/package/v<BRANCHE>/main/x86_64/nodejs`, et le dépôt `community` pour `npm`) :

| Étiquette | `nodejs` | `npm` | Satisfait `>= 22.19.0` ? |
|-----------|----------|-------|--------------------------|
| `alpine:3.20` (**actuelle**) | 20.15.1-r0 | 10.9.1-r0 | **Non** |
| `alpine:3.21` | 22.23.2-r0 | 10.9.1-r0 | Oui |
| `alpine:3.22` | 22.23.2-r0 | 11.6.4-r0 | Oui (**retenue**) |
| `alpine:3.23` | 24.18.1-r0 | 11.x | Oui, mais saut de version majeure de Node non nécessaire |

**Décision** : `alpine:3.22`, qui est le plus petit changement satisfaisant la contrainte tout en apportant un `npm` récent. `alpine:3.20` ne la satisfait pas du tout.

**Exigence de vérification** : ces valeurs doivent être **revalidées par une construction d'image réelle** au moment de l'implémentation, et pas reprises de ce tableau. Les index de paquets Alpine évoluent, et une contrainte `engines` non satisfaite se manifeste par un échec au premier lancement de l'agent, donc en production. La construction doit se terminer par un `pi --version` exécuté dans l'image.

Ajouts prévus à l'étage d'exécution :

```dockerfile
FROM alpine:3.22
RUN apk add --no-cache ca-certificates pandoc nodejs npm
RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent@0.84.2
ENV PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0 PI_CODING_AGENT_DIR=/data/pi
```

`--ignore-scripts` est la forme d'installation recommandée par le projet pi. **Coût approximatif** : de l'ordre de 60 à 80 Mo ajoutés à une image qui contient aujourd'hui un binaire Go statique, pandoc et typst. À mesurer précisément lors de l'implémentation (NF-8).

### 9.2 Pourquoi le même conteneur et pas un sidecar

| Argument | Détail |
|----------|--------|
| pi RPC est stdio seulement | Il n'y a **aucun** écouteur réseau : le protocole est du JSONL sur `stdin`/`stdout`. Un sidecar exigerait donc d'écrire un adaptateur stdio-vers-réseau, c'est-à-dire du code nouveau des deux côtés, à maintenir, pour un MVP |
| Le paquet serveur de pi est expérimental | `@earendil-works/pi-server` est auto-décrit « experimental server package for pi » : ce n'est pas une base sur laquelle on pose un pilote |
| Les fichiers | pi doit voir le même `WORKSPACE_DIR` que le backend. Dans le même conteneur c'est gratuit ; en sidecar il faut un volume partagé et une discipline de permissions |
| L'utilisateur | Un seul enseignant, un seul workspace : le bénéfice d'isolation d'un sidecar est faible aujourd'hui |

### 9.3 Les variantes v2 et leurs déclencheurs

| Variante | Ce qu'elle apporte | Déclencheur |
|----------|--------------------|-------------|
| Sidecar pi dans le même pod | Image backend redevenue légère, limites de ressources et profil `seccomp` indépendants, redémarrage de l'agent sans redémarrer l'API | Le multi-utilisateur, ou un besoin de limites de ressources distinctes, ou la stabilisation du paquet serveur de pi |
| Service Node séparé embarquant le SDK (`createAgentSession`, `ModelRuntime`, `SessionManager`) | Plus de sous-processus par demande, contrôle fin du cycle de vie des sessions | Un besoin de sessions longues et concurrentes, c'est-à-dire le v2 de 5.4 |
| Bac à sable par utilisateur (conteneur ou micro-VM par session) | Vraie isolation, conforme aux recommandations de pi pour du travail non surveillé | Le jour où l'application accueille plusieurs enseignants, ou du travail d'agent non supervisé |

### 9.4 Variables d'environnement

Nouvelles, toutes lues via l'assistant `envOr()` existant de `main.go`, avec le drapeau correspondant :

| Drapeau | Variable | Défaut | Rôle |
|---------|----------|--------|------|
| `--pi-enabled` | `PI_ENABLED` | `false` | Interrupteur maître. À `false`, ni route ni mode `Pi` : comportement actuel exact à deux modes (EX-13) |
| `--pi-bin` | `PI_BIN` | `pi` | Chemin du binaire, remplacé par un faux binaire dans les tests |
| `--pi-model` | `PI_MODEL_ID` | vide | Identifiant de modèle déclaré dans `models.json`. **Pas** `PI_MODEL` : ce nom appartient à pi (5.2) |
| `--pi-tools` | `PI_TOOLS` | `read,write,edit,grep,find,ls` | Liste d'outils autorisés. `bash` volontairement absent (8d) |
| `--pi-state-dir` | `PI_STATE_DIR` | `/data/pi` | Où le backend écrit `models.json` et `settings.json`, et où pi garde son état |
| `--bifrost-url` | `BIFROST_URL` | `http://bifrost:8080/v1` | Passerelle LLM |
| `--bifrost-key` | `BIFROST_API_KEY` | vide | Clé, passée par `strings.TrimSpace` à l'analyse des drapeaux, exactement comme `HERMES_API_KEY` |

À supprimer au même moment de `docker-compose.yml`, `.env.example` et du tableau d'environnement de `README.md` : `ACP_AGENT_CMD`, `OPENCODE_BASE_URL`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` (section 2).

### 9.5 Intégration continue

Aucun nouveau job. pi arrive **dans l'image backend existante**, donc les 4 jobs actuels de `.github/workflows/ci.yml` (`test-backend`, `test-frontend`, `build-backend`, `build-frontend`) restent tels quels. Deux réserves à énoncer :

1. La construction de l'image backend devient plus lourde (installation de `nodejs`, `npm` et d'un paquet npm global). Le temps de job augmente ; le cache de couches l'absorbe en partie.
2. Les tests du pont pi doivent tourner **sans réseau et sans vrai pi**, sinon `test-backend` devient dépendant du registre npm. D'où le faux binaire de la section 10, étape 1.

### 9.6 Kubernetes et ArgoCD

| Sujet | Décision |
|-------|----------|
| Clé Bifrost | Livrée comme secret. Le `\n` final est traité côté Go (6.2) : c'est la seule protection sur laquelle compter |
| Volume d'état | `/data/pi` doit être **inscriptible** (`models.json`, `settings.json`, cache de pi). Un volume dédié, pas le workspace |
| Sessions | `.pi-sessions` vit dans le workspace, donc sur le volume déjà persistant, et reste invisible dans l'arborescence (5.1) |
| Limites de ressources | Un processus Node par demande, chacun pouvant vivre jusqu'à `jobRunTimeout` (10 min) : la mémoire du pod doit tenir compte de plusieurs processus simultanés |
| Plafond de concurrence | Nombre de processus pi simultanés **plafonné** dans le pont (NF-9). Au-delà, la demande est refusée avec un `StreamEvent` d'erreur en français plutôt que de faire tomber le pod |
| `/health` | Doit rester répondable pendant un job d'agent (NF-7). C'est déjà le cas par construction, le job tournant dans une goroutine, mais c'est un point à vérifier sous charge |
| Programmes | `/data/programmes` monté en lecture seule (8f) |

### 9.7 Version et retour arrière

- **Version** : l'implémentation sera un incrément mineur (ajout de fonctionnalité rétrocompatible), à faire dans un commit `release:` conformément à `AGENTS.md`. **Ce document ne bump rien** : `version.json` reste inchangé.
- **Retour arrière** : ne pas configurer pi. Pas de `PI_ENABLED`, donc pas de route, donc `/api/agents` ne renvoie qu'une entrée, donc pas de mode `Pi`, donc l'application se comporte **exactement** comme aujourd'hui avec ses deux modes. C'est une propriété de conception à préserver activement, pas une conséquence heureuse : chaque incrément du plan ci-dessous doit être vérifié aussi dans la configuration « pi absent ».

---

## 10. 🗺️ Plan de mise en oeuvre

> Ce plan décrit une **tâche ultérieure**. Rien ici n'est à faire dans le cadre de ce document, qui ne produit que les deux fichiers de spécification.

Cinq incréments, chacun livrable et vérifiable seul.

### Étape 1 : le pont pi et la traduction d'événements

- **(nouveau)** `backend/internal/bridge/pi.go` : `PiBridge`, lancement du sous-processus, lecture JSONL, traduction vers `StreamEvent` (annexe).
- **(nouveau)** `backend/internal/bridge/pi_test.go` avec un **faux binaire pi** désigné par `PI_BIN`, dans l'esprit de `mockHermesServer()` : il lit une ligne de commande sur `stdin` et recrache une séquence d'événements JSONL fixée.
- C'est l'occasion de rendre réelle la cible `mock-agent` du `Makefile`, en créant enfin **(nouveau)** `backend/internal/bridge/mockagent`, ou de supprimer la cible (2.2).

**Preuve que ça marche** : trois tests calqués sur les trois existants, qui passent sans réseau et sans vrai pi : un échange complet avec `delta` puis `done`, une reconnexion avec `{type:'subscribe', jobId, after}` qui rejoue le bon suffixe, et un processus qui meurt en cours de flux qui produit un `error` portant `Échec IA. Réessaie.`. Et surtout : les trois tests de `hermes_test.go` inchangés et verts.

### Étape 2 : la route conditionnelle et le point d'entrée de découverte

- Enregistrement de `/ws/agent/pi` dans `main.go` sous condition, avec l'avertissement symétrique de celui de Hermes.
- **(nouveau)** `GET /api/agents`.

**Preuve** : sans `PI_ENABLED`, `/ws/agent/pi` n'est pas enregistré et `/api/agents` ne renvoie qu'une entrée ; avec, deux entrées ; `/ws/acp` répond à l'identique dans les deux cas.

### Étape 3 : emballage de pi dans l'image

- `backend/Dockerfile` : `alpine:3.22`, `nodejs`, `npm`, paquet pi épinglé, variables `PI_*`.

**Preuve** : `docker build` réussit, `pi --version` répond **dans** l'image, la taille avant/après est notée, et le binaire Go démarre toujours sans pi configuré.

### Étape 4 : le troisième mode côté frontend

Découpée en deux sous-étapes, parce que la première est un remaniement à comportement constant et qu'on veut pouvoir la fusionner seule :

**4a — étendre le mode sans ajouter d'agent.** `AppMode` gagne `'pi'`, `loadMode()` l'accepte, `Toolbar.tsx` affiche le troisième bouton, et `App.tsx` introduit `isWorkspace` et `chatAgent` (7.1) en remplaçant les deux ternaires `mode === 'desk' ?`. À ce stade `chatAgent` vaut toujours `'lya'` : **le mode pi existe, ressemble à Desk et se comporte exactement comme Desk.** Rien ne doit changer visuellement en modes Desk et Lya.

**Preuve 4a** : `Toolbar.test.tsx` étendu — trois boutons rendus, le clic appelle `onModeChange('pi')` ; un test qui vérifie qu'en mode `'pi'` l'arborescence et l'éditeur sont rendus (donc que le layout n'a pas été dupliqué ni perdu) ; vitest global toujours vert sur les 52 tests existants.

**4b — brancher pi.** `chatAgent` devient `mode === 'pi' ? 'pi' : 'lya'`, `Chat.tsx` dérive son chemin WebSocket de la propriété `agent` (la ligne 237), relecture du fichier et rafraîchissement de l'arborescence après une écriture (7.6), clés i18n de 7.2 dans `fr.json` **et** `en.json`, masquage du bouton `Pi` et repli du mode mémorisé si `/api/agents` n'annonce pas pi (7.3).

**Preuve 4b** : `Chat.test.tsx` étendu pour vérifier que l'URL WebSocket suit la propriété `agent` (`/ws/acp` contre `/ws/agent/pi`) ; un test du repli `'pi'` → `'desk'` quand pi n'est pas annoncé ; un test de la relecture de fichier après un `done` dont le flux contenait un appel d'outil d'écriture.

### Étape 5 : l'instrumentation

- La ligne `agent_usage` pour les deux agents (7.7), puis, seulement si le besoin se confirme, **(nouveau)** `POST /api/usage`.

**Preuve** : une session de travail réelle produit des lignes exploitables pour les deux agents, et une relecture de ces lignes montre qu'elles ne contiennent ni contenu de cours ni identifiant de personne.

---

## 📎 Annexe : correspondance des événements

Côté application, le contrat ne change pas d'un octet : `StreamEvent` reste `{seq, type, text, reply, error, detail, tool, jobId}` avec `type` parmi `delta | tool | done | error | meta`. Côté pi, la source est [docs/rpc.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md) et [docs/json.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md).

### Commande envoyée à pi

Le message entrant de l'application est `{type:'prompt', content, system}`. Le pont écrit sur `stdin` de pi :

```json
{"id":"<jobId>","type":"prompt","message":"<preambule pedagogique>\n\n---\n\n<contenu utilisateur>"}
```

Noter le nom du champ : pi attend `message`, pas `content`. Le préambule pédagogique est le contenu de `system` (voir D9 et Questions ouvertes).

### Événements pi vers `StreamEvent`

| Événement pi | `StreamEvent` produit | Détail |
|--------------|-----------------------|--------|
| `response` avec `command:"prompt"`, `success:true` | aucun | Simple accusé d'acceptation. Le `meta` portant le `jobId` a déjà été émis par le pont à la création du `Job` |
| `response` avec `success:false` | `error` | `error` = `Échec IA. Réessaie.`, `detail` = le champ `error` de pi |
| `agent_start` | aucun | Début de traitement ; rien à afficher |
| `turn_start`, `turn_end` | aucun | Granularité interne |
| `message_start` | aucun | Le pont initialise son accumulateur de texte |
| `message_update` avec `assistantMessageEvent.type = "text_delta"` | **`delta`** | `text` = `assistantMessageEvent.delta`. C'est le seul événement qui produit du texte visible |
| `message_update` avec `text_start` / `text_end` | aucun | `text_end.content` sert de contrôle de cohérence de l'accumulateur |
| `message_update` avec `thinking_start` / `thinking_delta` / `thinking_end` | aucun (MVP) | Le raisonnement n'est pas montré à l'enseignant. Pourrait devenir un `tool` d'information plus tard |
| `message_update` avec `toolcall_start` (porte `id` et `toolName`) | **`tool`** | `tool` = `{phase:"start", id, name}` : le champ `Tool` de `StreamEvent` est déjà `any` et `Chat.tsx` sait déjà afficher un événement d'outil |
| `message_update` avec `toolcall_delta` / `toolcall_end` | aucun | Les arguments accumulés n'apportent rien à l'utilisateur ; `tool_execution_*` suffit |
| `tool_execution_start` | **`tool`** | `{phase:"exec", toolCallId, name, args}` : les `args` d'un `write`/`edit` contiennent le chemin visé, utile à afficher |
| `tool_execution_update` | aucun | `partialResult` est cumulatif et volumineux : ignoré pour ne pas gonfler le journal du `Job`, qui est conservé en mémoire jusqu'à `jobTTL` |
| `tool_execution_end` | **`tool`** | `{phase:"end", toolCallId, name, isError}`. Alimente aussi les compteurs `tools` / `toolsUsed` de la ligne de mesure et le drapeau « un fichier a été modifié » de 7.6 |
| `message_end` | aucun | La documentation dit de traiter `message_end.message` comme **autoritatif** : c'est de là que le pont tire le texte final, pas de la somme des deltas |
| `agent_end` | aucun | Si `willRetry` est `true`, un nouvel essai suit : ne surtout pas émettre `done` ici |
| `agent_settled` | **`done`** | Fin réelle : plus de reprise automatique, ni de compaction, ni de message en file. `reply` = texte assistant final accumulé |
| `auto_retry_start` / `auto_retry_end` | aucun | Journalisé côté backend seulement. Un `error` prématuré serait faux, puisque pi réessaie tout seul |
| `compaction_start` / `compaction_end` | aucun | Journal seulement |
| `summarization_retry_scheduled` / `_attempt_start` / `_finished` | aucun | Journal seulement |
| `extension_error` | aucun | Journal seulement : aucune extension n'est chargée (8b) |
| `queue_update` | sans objet | Le pont n'envoie ni `steer` ni `follow_up` en MVP |
| `bash_execution_update` | sans objet | Le pont n'envoie jamais la commande RPC `bash`, et l'outil `bash` est exclu (8d) |
| `extension_ui_request` | aucun | Ne devrait jamais arriver. **Sécurité anti-blocage** : si un dialogue arrive quand même (`select`, `confirm`, `input`, `editor`), le pont répond immédiatement `{"type":"extension_ui_response","id":"<id>","cancelled":true}`, sinon pi attend jusqu'au délai de 10 minutes |
| Ligne d'en-tête de session `{"type":"session","version":...}` | aucun | Documentée pour `--mode json`. Sa présence en `--mode rpc` est **à confirmer** : le pont doit de toute façon ignorer silencieusement tout type inconnu |
| Tout autre type | aucun | Ignoré, et journalisé **une seule fois** par type et par job pour ne pas noyer le journal |

### Construction de la réponse complète

1. Pendant le flux, le pont accumule les `text_delta` par `contentIndex` (le protocole ne fournit plus d'instantané cumulatif : c'est au client d'assembler).
2. À `message_end`, il remplace son accumulateur par les blocs de texte du message, qui font foi.
3. À `agent_settled`, il émet `done` avec `reply` = ce texte. `Chat.tsx` utilise déjà `reply` pour figer le message final.

### Mort du processus en cours de flux

| Cas | Traitement |
|-----|------------|
| Sortie non nulle, ou `stdout` fermé avant `agent_settled` | `StreamEvent{Type:"error", Error:"Échec IA. Réessaie.", Detail:"pi: exit N, <dernières lignes de stderr, tronquées>"}` |
| Expiration de `jobRunTimeout` | Même forme, `Detail` indiquant l'expiration du délai de 10 minutes |
| JSONL illisible sur une ligne | La ligne est ignorée et comptée ; si aucune ligne valide n'arrive du tout, on retombe sur le cas « sortie » ci-dessus |

Le message utilisateur est **exactement** celui que le pont Hermes produit déjà (`Échec IA. Réessaie.`) : l'enseignant voit le même message quel que soit l'agent, et le détail technique reste dans `detail`. `stderr` est capturé séparément, tronqué, et n'entre jamais dans `reply`.

---

## ❓ Questions ouvertes

Ce qui ne peut pas être décidé depuis le dépôt, et qui doit l'être avant ou pendant l'implémentation.

| # | Question | Ce qu'il faut pour trancher |
|---|----------|----------------------------|
| Q1 | Quel identifiant de modèle déclarer dans `models.json`, et quels `contextWindow` / `maxTokens` réels ? | Interroger le Bifrost déployé (liste de modèles) et retenir un modèle dont la qualité en anglais pédagogique est vérifiée sur deux ou trois demandes typiques. Les valeurs par défaut de pi (`contextWindow` 128000, `maxTokens` 16384) sont des valeurs de repli, pas une mesure |
| Q2 | Bifrost a-t-il besoin d'un drapeau `compat` ? | Sondage empirique décrit en 6.4 : aucun drapeau au départ, on n'en ajoute qu'en réponse à un symptôme observé |
| Q3 | Le prompt système pédagogique : skill pi ou tour de préambule ? | **Décision MVP : tour de préambule** (D9). Les trois options et leurs compromis : **(a) préambule** : aucune infrastructure, suit le niveau choisi à l'exécution, mais consomme des jetons à chaque demande et se mélange au message utilisateur ; **(b) skill monté dans l'image** (`--skill`) : propre, chargé à la demande par le modèle, mais **statique**, donc incapable de suivre le niveau et les données de programme qui changent à l'exécution, et il faudrait le régénérer à chaque changement de niveau ; **(c) `--system-prompt`** : remplace le prompt par défaut de pi, **y compris ses instructions d'usage des outils**, ce qui casserait précisément la capacité qu'on veut. À revoir si le préambule s'avère coûteux en jetons |
| Q4 | Historique partagé ou séparé entre les deux agents ? | **Décision MVP : séparé, sans rejeu croisé** (D11), parce que le protocole actuel est déjà sans historique. À revalider auprès du pilote : s'il attend qu'un agent « se souvienne » de l'échange mené avec l'autre, c'est une fonctionnalité nouvelle à spécifier, pas un réglage |
| Q5 | Le mode RPC émet-il une ligne d'en-tête de session ? | À confirmer par observation sur un pi réel. Sans effet sur la conception : tout type inconnu est ignoré |
| Q6 | Quel est le coût réel du démarrage d'un processus Node par demande ? | À mesurer à l'étape 3 du plan. S'il est significatif devant la latence LLM, le v2 « processus long-lived » de 5.4 remonte dans les priorités |
| Q7 | Quel plafond de processus pi simultanés ? | Dépend de la mémoire allouée au pod. À fixer à l'étape 3, avec une valeur basse au départ (le produit a un seul utilisateur) |

---

## 🔗 Références

**pi**

- [Dépôt earendil-works/pi](https://github.com/earendil-works/pi) (l'ancien [badlogic/pi-mono](https://github.com/badlogic/pi-mono) redirige vers lui)
- [pi.dev](https://pi.dev)
- [Index de la documentation du coding agent](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/index.md)
- [docs/rpc.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md) : protocole JSONL, commandes, événements
- [docs/json.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md) : flux d'événements JSON
- [docs/models.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md) : `models.json`, résolution des valeurs, drapeaux `compat`
- [docs/custom-provider.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/custom-provider.md) : fournisseurs personnalisés, `authHeader`, types d'API
- [docs/containerization.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/containerization.md) : patrons d'isolation
- [docs/security.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md) : confiance projet, absence de bac à sable
- [docs/environment-variables.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/environment-variables.md) : variables lues et exportées par pi
- [docs/skills.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/skills.md) : format des skills (option v2 de Q3)
- [docs/settings.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/settings.md) : `defaultProjectTrust`
- [docs/sdk.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sdk.md) : SDK, variante v2 de 9.3
- [Paquet npm @earendil-works/pi-coding-agent](https://www.npmjs.com/package/@earendil-works/pi-coding-agent)

**Le chemin ACP, examiné puis écarté**

- [agentclientprotocol.com](https://agentclientprotocol.com)
- [Bibliothèques officielles](https://agentclientprotocol.com/libraries/typescript) : TypeScript, Rust, Python, Kotlin, Java
- [Bibliothèques communautaires](https://agentclientprotocol.com/libraries/community) : les seules implémentations Go, toutes non officielles
- [Adaptateur ACP tiers pour pi](https://github.com/svkozak/pi-acp)

**LLM**

- [Bifrost](https://github.com/maximhq/bifrost)
- [Documentation Bifrost](https://docs.getbifrost.ai)

**Dans le dépôt**

- [AGENTS.md](../../../AGENTS.md) : conventions, langue de l'interface, procédure de release
- [docs/etude-faisabilite-ide-web/architecture-v2.md](../../../docs/etude-faisabilite-ide-web/architecture-v2.md) : étude d'architecture antérieure, où figurent la promesse ACP et le choix de Bifrost
- [requirements.md](requirements.md) : les exigences que ce design met en oeuvre

---

*Design : second agent pi + Bifrost. Deux agents, trois modes (`Desk` / `Pi` / `Lya`), et une question tranchée par l'usage plutôt que par le raisonnement.*
