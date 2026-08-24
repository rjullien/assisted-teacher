# Second agent : pi + Bifrost (exigences)

> Spec produit. Le « comment » est dans [design.md](design.md).
> Statut : specification, aucune ligne de code écrite. Cible d'implémentation : une tâche ultérieure.

---

## 🎯 Contexte

L'application n'a aujourd'hui qu'un seul chemin d'agent : le pont Hermes/Lya, exposé sur la route WebSocket `/ws/acp` du backend Go et consommé par `Chat.tsx` (mode Desk) et `LyaChat.tsx` (mode Lya). Hermes/Lya est un agent hébergé, autonome, avec mémoire et skills, atteint par un seul appel HTTPS : c'est un très bon compagnon de conversation, mais il ne touche jamais aux fichiers de cours, et tout ce qu'il produit n'arrive dans le cours que si l'enseignant clique sur « 📝 Insérer dans le cours ». La question posée par le pilote est donc légitime : pour écrire des cours d'anglais, un agent hébergé avec mémoire et skills est-il l'outil adapté, ou est-il surdimensionné ? Personne ne peut répondre à cette question par le raisonnement seul. On ajoute donc un **second agent, plus léger et local (pi piloté par le backend, adossé à Bifrost pour le LLM)**, et on l'expose comme un **troisième mode de premier niveau** (`Desk` / `Pi` / `Lya`) dans le sélecteur de mode qui existe déjà, et c'est l'usage réel du pilote qui tranche. Ce document décrit ce que le second agent doit faire et comment on saura, à la fin du pilote, lequel des deux garder.

---

## 🚦 Objectifs

| # | Objectif |
|---|----------|
| O1 | Ajouter un second agent, `pi` en mode RPC, piloté par le backend Go, dont le LLM est servi par Bifrost |
| O2 | Rendre les deux agents disponibles **en même temps** via trois modes sélectionnables depuis l'interface, sans redémarrage ni fichier de configuration |
| O3 | Ne rien casser : `/ws/acp` et le comportement actuel du chat restent identiques |
| O4 | Donner à pi la capacité d'agir sur les fichiers de cours, avec une frontière explicite |
| O5 | Instrumenter l'usage des deux agents pour répondre, mesures en main, à la question « Hermes est-il overkill ? » |

## 🚫 Non-objectifs

| # | Non-objectif | Pourquoi c'est hors périmètre |
|---|--------------|-------------------------------|
| N1 | Isolation multi-utilisateurs | Le produit est mono-enseignant. Un `WORKSPACE_DIR` unique, une seule identité derrière Authelia. Le multi-utilisateur reste une cible v2 (voir `docs/etude-faisabilite-ide-web/architecture-v2.md`) |
| N2 | Synchronisation GitHub des cours | Aucun lien avec le choix d'agent |
| N3 | Implémenter réellement ACP | pi parle son propre protocole JSONL. La promesse ACP de `README.md` n'a jamais été construite : elle est retirée, pas honorée (voir design, décision 2) |
| N4 | Supprimer ou remplacer Hermes/Lya maintenant | Le but du dispositif est précisément de ne pas préjuger. La décision de retirer un des deux agents est un résultat du pilote, pas une prémisse |
| N5 | Remplacer le rendu, l'export PDF/DOCX, l'éditeur ou le panneau programme | Inchangés |
| N6 | Bump de version, image publiée, déploiement | Ce document est une spec ; `version.json` reste inchangé |

---

## 👤 Utilisateur pilote

Un enseignant d'anglais, non développeur, seul utilisateur du produit. Conséquences directes, qui sont des contraintes de conception et pas des préférences :

1. Le choix d'agent est **un seul contrôle visible** dans la barre d'outils, avec des libellés en langue naturelle. Pas de variable d'environnement, pas de fichier à éditer, pas de redémarrage.
2. Le contrôle n'apparaît que s'il y a réellement quelque chose à choisir. Un agent non configuré côté serveur ne doit jamais être proposé : un bouton qui mène à une erreur est pire que pas de bouton.
3. Les messages d'erreur restent en français et restent les mêmes que ceux déjà connus de l'utilisateur (le backend produit déjà `Échec IA. Réessaie.` avec le détail technique séparé).
4. Le vocabulaire de l'interface reste français par défaut, anglais disponible via `useI18n()`, conformément à `AGENTS.md`.

---

## 📋 Exigences fonctionnelles

Chaque exigence a un critère d'acceptation formulé comme un résultat observable.

### EX-1 : trois modes sélectionnables à l'exécution

L'enseignante choisit sa façon de travailler depuis le sélecteur de mode existant, sans rechargement forcé ni redémarrage du serveur. Le mode `pi` s'ajoute aux modes `desk` et `lya` **au même niveau** : il n'y a pas de second sélecteur.

**Critère** : la barre d'outils présente trois boutons, `Desk`, `Pi` et `Lya`. En mode `Pi`, l'écran est le même qu'en mode `Desk` — arborescence, éditeur, chat — et envoyer un message produit une réponse issue de pi, visible dans le journal du backend par une ligne `agent=pi mode=pi`. En mode `Desk`, la même action produit une réponse issue de Hermes, `agent=lya mode=desk`. En mode `Lya`, l'écran plein est inchangé et le journal porte `agent=lya mode=lya`.

### EX-2 : le choix survit au rechargement

**Critère** : après sélection du mode `Pi`, rechargement complet de la page (F5), le mode `Pi` est toujours actif et le message suivant part vers pi. Aucun autre réglage (niveau, langue) n'est modifié par l'opération. Aucune clé de `localStorage` nouvelle n'est créée : `assisted-teacher-mode` accepte simplement une troisième valeur.

### EX-2b : un mode inconnu ou indisponible retombe sur Desk

**Critère** : si `localStorage` contient `pi` alors que le serveur n'annonce pas pi dans `GET /api/agents`, l'application démarre en mode `Desk` et le bouton `Pi` n'est pas rendu. Même comportement pour toute valeur inconnue. C'est ce qui rend EX-13 vrai sans intervention manuelle sur les navigateurs.

### EX-3 : le backend annonce les agents réellement disponibles

L'interface ne propose jamais un agent que le serveur n'a pas configuré.

**Critère** : avec seulement la clé Hermes configurée, le bouton `Pi` n'est pas rendu et l'interface se comporte exactement comme la version actuelle (deux modes). Avec les deux configurés, trois boutons de mode sont rendus. Avec seulement pi configuré, le mode `Desk` route vers pi et le mode `Lya` est masqué. Dans les trois cas, aucun appel réseau ne part vers une route non enregistrée.

### EX-4 : `/ws/acp` reste inchangé

**Critère** : un client qui ouvre `/ws/acp` et envoie `{"type":"prompt","content":...,"system":...}` reçoit exactement la même séquence d'événements qu'avant l'ajout de pi. Les trois tests existants de `backend/internal/bridge/hermes_test.go` (`TestHermesBridge_PromptAndStream`, `TestHermesBridge_Reconnect`, `TestHermesBridge_BadKey`) passent sans modification d'une seule ligne de test.

### EX-5 : un navigateur avec un bundle en cache continue de fonctionner

**Critère** : un navigateur qui sert encore l'ancien bundle JavaScript (donc qui ignore tout du sélecteur et appelle `/ws/acp` en dur) garde un chat pleinement fonctionnel après déploiement.

### EX-6 : streaming identique quel que soit l'agent

Le contrat de fil (`StreamEvent` : `seq`, `type` parmi `delta|tool|done|error|meta`, `text`, `reply`, `error`, `detail`, `tool`, `jobId`) est le même pour les deux agents. Le frontend n'a pas de second moteur de rendu.

**Critère** : la même conversation menée avec chacun des deux agents produit deux flux d'événements de même forme : un `meta` portant le `jobId`, une suite de `delta`, un `done` final portant la réponse complète accumulée dans `reply`. Aucun champ nouveau n'est ajouté à `StreamEvent`.

### EX-7 : rejeu identique après coupure

**Critère** : pendant qu'une réponse de pi est en cours, la connexion WebSocket est coupée côté client ; à la reconnexion, le client envoie `{"type":"subscribe","jobId":"...","after":N}` et reçoit la suite du flux à partir de `seq > N`, sans perte et sans doublon, jusqu'au `done`. Le processus pi n'a pas été tué par la coupure.

### EX-8 : pi peut lire et écrire les fichiers de cours, dans une frontière explicite

pi doit pouvoir modifier directement un fichier de cours (par exemple ajouter une section à une unité) : c'est la différence de nature avec Hermes. Le périmètre de ce qu'il peut toucher est une exigence, pas un détail d'implémentation.

**Critère** : (a) une demande de type « ajoute un exercice de vocabulaire à la fin de ce fichier » aboutit à un fichier modifié sur disque dans le workspace, sans intervention de l'utilisateur ; (b) toute tentative d'écriture hors du répertoire de travail échoue ; (c) le répertoire des programmes officiels n'est pas modifiable par l'agent ; (d) l'inventaire des outils réellement accordés à pi est écrit dans la configuration de déploiement et vérifiable dans le journal de démarrage.

### EX-9 : quand pi a modifié un fichier, l'interface le reflète

Proposer « Insérer dans le cours » après que l'agent a lui-même écrit dans le fichier n'a aucun sens et conduirait à dupliquer le contenu.

**Critère** : après un `done` d'un échange pi qui a modifié le fichier ouvert, l'éditeur affiche le contenu à jour sans que l'utilisateur ait à recharger la page ni à rouvrir le fichier, et le bouton « Insérer dans le cours » n'est pas proposé pour ce message. Si pi a créé un nouveau fichier, l'arborescence de gauche le montre.

### EX-10 : le prompt système pédagogique s'applique aussi à pi

Le contexte pédagogique (niveau, données du programme officiel) que `Chat.tsx` construit aujourd'hui via `buildSystemPrompt()` conditionne la qualité de la réponse. Il ne doit pas disparaître quand on bascule sur pi.

**Critère** : à niveau égal et sur la même demande, la réponse de pi mentionne le niveau et les repères du programme sélectionné, comme le fait aujourd'hui celle de Hermes.

### EX-11 : Bifrost reste l'unique sortie LLM

**Critère** : aucun secret de fournisseur LLM (Anthropic, OpenAI ou autre) n'est présent dans l'environnement du conteneur ; la seule clé d'accès modèle est celle de Bifrost ; couper Bifrost fait échouer l'agent pi avec un message d'erreur en français, et n'ouvre aucun chemin de repli vers un fournisseur direct.

### EX-12 : l'usage par agent est observable

À la fin du pilote, il doit être possible de dire lequel des trois modes a été réellement utilisé, sur quel type de demandes, et avec quel taux d'échec. Les modes `Desk` et `Lya` appelant tous deux Hermes, la mesure doit les distinguer : la ligne de journal porte donc `agent` **et** `mode`.

**Critère** : pour chaque demande, une ligne de journal structurée du backend permet de compter, par agent : le nombre de demandes, la durée, le statut final, la longueur de la demande, le fichier visé, et pour pi le nombre d'appels d'outils. Ces lignes ne contiennent ni contenu de cours ni identifiant de personne.

### EX-13 : retour arrière trivial

**Critère** : dans un déploiement où pi n'est pas configuré, l'application se comporte **exactement** comme la version actuelle : deux modes seulement, pas de route supplémentaire, pas de processus enfant, aucune dépendance nouvelle sollicitée au démarrage.

---

## 🛡️ Exigences non fonctionnelles

| # | Exigence | Critère observable |
|---|----------|--------------------|
| NF-1 | Réactivité perçue : le premier fragment de réponse arrive sans attendre la réponse complète | Un premier événement `delta` est reçu par le navigateur strictement avant l'événement `done`, sur les deux agents |
| NF-2 | Résistance aux coupures : une réponse longue survit au délai d'expiration de 100 s du reverse proxy | Une demande dont le traitement dépasse 100 s se termine correctement, la réponse étant récupérée par `subscribe` après reconnexion (mécanisme de job détaché déjà en place, `jobTTL` 15 min, `jobRunTimeout` 10 min) |
| NF-3 | Aucun secret dans un journal | Les journaux contiennent au plus l'URL, la longueur de la clé et une empreinte non réversible de 8 caractères hexadécimaux, jamais la clé. Vérifiable par relecture des lignes de démarrage et des lignes d'échec 401/403 |
| NF-4 | Pas de régression pour un bundle en cache | Voir EX-5 |
| NF-5 | Confidentialité de la mesure | Les lignes de mesure sont techniques et agrégeables : pas de contenu de cours, pas de nom, pas d'adresse de courriel, pas d'identifiant d'utilisateur |
| NF-6 | Confinement de l'agent | Aucune écriture possible hors du répertoire de travail ; le répertoire des programmes est monté en lecture seule ; l'agent tourne sous un compte non privilégié |
| NF-7 | Disponibilité pendant un traitement | `/health` répond pendant qu'un job d'agent est en cours ; un job en cours ne bloque ni la lecture ni l'écriture de fichiers via l'API REST |
| NF-8 | Coût de l'image maîtrisé | L'augmentation de taille de l'image backend est mesurée et documentée lors de l'implémentation ; la construction reste faite par la CI existante sans nouveau job |
| NF-9 | Nombre de processus borné | Le nombre de processus d'agent simultanés est plafonné, compte tenu du délai d'expiration de 10 minutes par job |

---

## 📊 Critères de succès du pilote

Le dispositif n'a pas pour but de valider pi. Il a pour but de **répondre à une question**. Il réussit si, au bout de la période de pilote, la question « Hermes est-il overkill pour écrire des cours ? » a une réponse appuyée sur des observations.

### Signaux à collecter

| Signal | Source | Ce qu'il indique |
|--------|--------|------------------|
| Part des demandes par agent | ligne de journal par demande | La préférence révélée, indépendamment des déclarations |
| Évolution de cette part dans le temps | même source, par semaine | Effet de nouveauté ou préférence stable |
| Taux d'échec par agent | statut final de chaque ligne | Fiabilité comparée |
| Durée médiane par agent | durée de chaque ligne | Confort d'usage |
| Nombre de fichiers effectivement modifiés par pi | appels d'outils d'écriture | Est-ce que la capacité d'agir sert vraiment ? |
| Usage de « Insérer dans le cours » côté Hermes | frontend | Le circuit conversationnel suffit-il ? |
| Coût et volume de jetons | tableau de bord Bifrost déjà relié depuis la barre d'outils | Comparaison économique du chemin pi |

### Conditions de sortie

Le pilote est concluant quand les trois conditions suivantes sont réunies :

1. Au moins quatre semaines d'usage réel, avec des séances de préparation de cours effectives et pas seulement des essais.
2. Un volume de demandes suffisant pour que la répartition entre agents ne soit pas anecdotique, et au moins une demande d'écriture de fichier réellement acceptée par l'enseignant.
3. Aucun incident de sécurité ou de perte de contenu attribuable à l'agent pi resté non expliqué.

### Qui décide

**L'enseignant pilote décide**, parce qu'il est le seul utilisateur et que la question porte sur son usage. Les mesures ne votent pas : elles servent à ce que la décision ne repose pas seulement sur une impression. Les trois issues possibles, et les signaux qui les justifieraient, sont détaillées dans la section « Verdict du pilote » de [design.md](design.md).

---

## 🔗 Dépendances et hypothèses

| Hypothèse | Si elle est fausse |
|-----------|--------------------|
| Bifrost est déjà déployé et joignable depuis le backend, en API compatible OpenAI | pi ne peut pas être configuré : l'application retombe sur le comportement EX-13, sans mode `Pi` |
| Une clé d'accès Bifrost peut être livrée au conteneur comme secret | Idem |
| L'image backend peut embarquer un moteur Node 22 ou plus | Le second agent n'est pas embarquable dans le conteneur actuel : il faudrait passer par la variante de conteneur séparé documentée dans le design |
| Le workspace reste mono-utilisateur | Le confinement décrit en NF-6 devient insuffisant et il faut passer à une isolation par utilisateur |

---

*Exigences : second agent pi + Bifrost, exposé comme troisième mode, pour répondre par l'usage à la question de l'adéquation de Hermes.*
