---
name: web-search
description: USE FOR web search. Recherche sur le web via l'API Brave Search. À utiliser pour vérifier un fait, trouver un document authentique, une définition ou une référence récente avant de rédiger un cours.
---

# Recherche web (Brave Search)

La clé API est déjà dans l'environnement : `$BRAVE_SEARCH_API_KEY`. Ne la demande
pas et ne l'écris jamais dans un fichier.

## Recherche simple

```bash
curl -sS --max-time 10 "https://api.search.brave.com/res/v1/web/search" \
  -H "Accept: application/json" \
  -H "X-Subscription-Token: ${BRAVE_SEARCH_API_KEY}" \
  -G \
  --data-urlencode "q=VOTRE REQUÊTE" \
  --data-urlencode "count=5" \
  --data-urlencode "search_lang=fr" \
  --data-urlencode "text_decorations=false"
```

La réponse est du JSON. Les résultats utiles sont dans `web.results[]`, chacun
avec `title`, `url`, `description` et parfois `age`.

## Extraire uniquement l'essentiel

La réponse complète est volumineuse. Filtre-la pour ne garder que le nécessaire :

```bash
curl -sS --max-time 10 "https://api.search.brave.com/res/v1/web/search" \
  -H "Accept: application/json" \
  -H "X-Subscription-Token: ${BRAVE_SEARCH_API_KEY}" \
  -G --data-urlencode "q=VOTRE REQUÊTE" --data-urlencode "count=5" \
  | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const r=(JSON.parse(s).web||{}).results||[];r.slice(0,5).forEach((x,i)=>console.log(`${i+1}. ${x.title}\n   ${x.url}\n   ${x.description||""}\n`))})'
```

`node` est présent dans l'image ; `jq` ne l'est pas.

## Paramètres utiles

| Paramètre | Effet |
|---|---|
| `count` | Nombre de résultats, 1 à 20. Reste à 5 sauf besoin explicite. |
| `search_lang` | `fr` ou `en` selon la langue du contenu cherché. |
| `freshness` | `pd` (24 h), `pw` (7 j), `pm` (31 j), `py` (1 an). |
| `country` | Code pays à 2 lettres, ou `ALL`. |

## Opérateurs de recherche

`site:example.com`, `filetype:pdf`, `intitle:mot`, `"expression exacte"`,
`-motAExclure`.

## Si la recherche échoue

Codes à reconnaître dans la sortie :

- **429** : quota dépassé. N'insiste pas, ne relance pas en boucle.
- **401 / 403** : clé invalide ou absente.
- **timeout / erreur réseau** : Brave injoignable depuis le cluster.

Dans tous ces cas : **dis-le explicitement à l'enseignante, puis continue avec
ce que tu sais déjà.** Ne bloque pas la demande sur une recherche impossible, et
ne présente jamais une supposition comme un résultat de recherche.

## Règles

- Une recherche à la fois, jamais en parallèle : l'API limite le débit.
- Cite l'URL quand tu reprends une information trouvée en ligne.
- N'écris pas les résultats bruts dans un fichier de cours : reformule.
