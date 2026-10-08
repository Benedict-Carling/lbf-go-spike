# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root: the glossary of domain words (dataset, parent, instrument, property, profile, crate).
- **`README.md` "Design decisions"**: this repo's decision record, in place of `docs/adr/`. Read the decisions that touch the area you're about to work in.

## File structure

Single-context repo:

```
/
├── CONTEXT.md     ← glossary
├── README.md      ← "Design decisions" section is the decision record
├── AGENTS.md
└── *.go
```

## Recording decisions

New decisions go in `README.md` "Design decisions", each with its reason (as `AGENTS.md` requires). Don't create `docs/adr/`.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag decision conflicts

If your output contradicts a recorded design decision, surface it explicitly rather than silently overriding:

> _Contradicts the README design decision on crate versions, but worth reopening because…_
