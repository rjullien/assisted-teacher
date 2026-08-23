# Skill: Déploiement (Deploy)

## Description

Procédure standard pour releaser et déployer l'application **assisted-teacher** (frontend + backend).

## Prérequis

- Être sur la branche `main` avec un working tree propre (`git status` clean)
- Les tests CI doivent être verts (le pipeline bloque le push d'image si les tests échouent)
- Accès push au repo `rjullien/assisted-teacher`

## Versioning

Le projet suit **Semantic Versioning 2.0.0** (semver.org).

| Fichier | Rôle |
|---------|------|
| `version.json` (racine) | Source de vérité unique pour la version courante |

Format de `version.json` :
```json
{
  "version": "1.2.0"
}
```

### Règles d'incrémentation

| Type de changement | Bump | Exemple |
|-------------------|------|---------|
| Breaking change / incompatibilité API | **major** | 1.x.x → 2.0.0 |
| Nouvelle fonctionnalité rétrocompatible | **minor** | 1.2.x → 1.3.0 |
| Bugfix, refactor, perf, docs | **patch** | 1.2.3 → 1.2.4 |

## Procédure de release

### Étape 1 — Bump version

```bash
# Éditer version.json avec la nouvelle version
# Exemple : "1.2.0" → "1.3.0"
```

### Étape 2 — Commit + push sur main

```bash
git add version.json
git commit -m "release: v1.3.0"
git push origin main
```

### Étape 3 — Créer le tag semver + push

```bash
git tag v1.3.0
git push origin v1.3.0
```

### Étape 4 — Créer la GitHub Release

```bash
gh api repos/rjullien/assisted-teacher/releases \
  -f tag_name="v1.3.0" \
  -f name="v1.3.0" \
  -f body="## Changements\n\n- Description des changements" \
  -F generate_release_notes=true
```

## Pipeline CI (GitHub Actions)

Le workflow `.github/workflows/ci.yml` :

1. **Sur push `main`** : build + push images Docker taguées `main` + SHA
2. **Sur tag `v*`** : build + push images Docker taguées avec la version semver (ex: `1.3.0`)

Images publiées sur GHCR :
- `ghcr.io/rjullien/assisted-teacher/frontend:<tag>`
- `ghcr.io/rjullien/assisted-teacher/backend:<tag>`

## Déploiement effectif

Les images Docker sont publiées sur GitHub Container Registry (GHCR).
L'infrastructure (ArgoCD Image Updater ou équivalent) détecte automatiquement les nouvelles images et déploie.

**⚠️ IMPORTANT** : Ne JAMAIS créer un tag sans avoir d'abord bumpé `version.json`. La version dans le fichier est injectée dans le frontend au build (variable `APP_VERSION`) et sert de cache-buster.

## Rollback

```bash
# Revenir à une version précédente
git revert HEAD
git push origin main
# Ou : redéployer une image précédente via l'infra
```

## Vérification post-déploiement

1. Vérifier que le CI est vert : `gh api repos/rjullien/assisted-teacher/actions/runs --jq '.workflow_runs[0] | "\(.status) \(.conclusion)"'`
2. Vérifier la version affichée dans le footer de l'app (doit correspondre à `version.json`)
3. Tester les fonctionnalités critiques : édition fichier, chat IA, export PDF/DOCX
