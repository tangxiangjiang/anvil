# Design

Design is **versioned by product cutoff**. Each `vX.Y/` tree is the design snapshot that roadmaps through that line implement against.

## Version trees

| Tree | Role | Index |
|------|------|--------|
| [`v1.0/`](v1.0/) | **Current cutoff** — chapters 00–26 for delivery through product **0.26** | [v1.0/README.md](v1.0/README.md) |

Matching roadmaps: [`doc/roadmaps/v1.0/`](../roadmaps/v1.0/).

Future major design cuts (e.g. `v1.1/`, `v2.0/`) get a new directory; do not mix chapters across trees.

## Conventions

- Prefer linking `doc/design/v1.0/<chapter>.md` (or relative `../../design/v1.0/…` from `roadmaps/v1.0/` milestones).
- Do not recreate a flat `doc/design/00-*.md` set alongside version trees.
- New capability still needs a roadmap phase before code (see `.cursor/rules/doc-versioning.mdc`).
