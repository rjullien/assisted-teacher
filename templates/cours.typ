// Template Typst pour export de cours
// Personnalisable : changer les couleurs, polices, logo, etc.

#import "@preview/cmarker:0.2.1"

#set page(
  paper: "a4",
  margin: (top: 2.5cm, bottom: 2cm, left: 2cm, right: 2cm),
  header: align(right)[
    #text(size: 9pt, fill: luma(100))[Cours d'anglais]
  ],
  footer: align(center)[
    #counter(page).display("1 / 1", both: true)
  ],
)

#set text(
  font: "Source Sans Pro",
  size: 11pt,
  lang: "en",
)

#set heading(numbering: "1.1")

#show heading.where(level: 1): it => {
  set text(size: 22pt, weight: "bold", fill: rgb("#2563eb"))
  block(above: 2em, below: 1em)[#it.body]
}

#show heading.where(level: 2): it => {
  set text(size: 16pt, weight: "bold", fill: rgb("#1e40af"))
  block(above: 1.5em, below: 0.8em)[#it.body]
}

#show heading.where(level: 3): it => {
  set text(size: 13pt, weight: "bold")
  block(above: 1.2em, below: 0.6em)[#it.body]
}

// Tables styling
#set table(
  stroke: 0.5pt + luma(180),
  inset: 8pt,
)

// Code blocks
#show raw.where(block: true): block.with(
  fill: luma(240),
  inset: 10pt,
  radius: 4pt,
  width: 100%,
)

// Blockquotes
#show quote.where(block: true): block.with(
  stroke: (left: 3pt + rgb("#2563eb")),
  inset: (left: 12pt, y: 4pt),
)

// Content from markdown
// Usage: typst compile this file with --input md=path/to/file.md
#let md-content = read(sys.inputs.at("md", default: "content.md"))
#cmarker.render(md-content)
