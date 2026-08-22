# Templates

## `cours.typ`

Template Typst pour l'export PDF des cours. Personnalisable :
- Polices, couleurs, marges
- Header/footer (nom de l'école, logo)
- Numérotation des pages
- Style des tableaux, blockquotes, etc.

### Usage (CLI)

```bash
typst compile cours.typ output.pdf --input md=../workspace/B1/unit5.md
```

### Ajouter un logo

Placer un fichier `logo.png` dans ce dossier et ajouter dans `cours.typ` :

```typst
#set page(header: [
  #image("logo.png", height: 1cm)
  #h(1fr)
  #text(size: 9pt)[Mon école]
])
```

## `reference.docx` (optionnel)

Template Word pour l'export DOCX via Pandoc. Créer un .docx avec les styles souhaités et le placer ici.

Pandoc l'utilise automatiquement si présent :
```bash
pandoc input.md -o output.docx --reference-doc=reference.docx
```
