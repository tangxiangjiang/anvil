# Design

Design is **versioned by product cutoff**. Each `vX.Y/` tree is the design snapshot that roadmaps through that line implement against.

## Version trees

| Tree | Role | Index |
|------|------|--------|
| [`v0.x/`](v0.x/) | **Current cutoff** — chapters through product **0.50** | [v0.x/README.md](v0.x/README.md) |

Matching roadmaps: [`doc/roadmaps/v0.x/`](../roadmaps/v0.x/).

Future major design cuts (e.g. `v1.1/`, `v2.0/`) get a new directory; do not mix chapters across trees.

## Conventions

- Prefer linking `doc/design/v0.x/<chapter>.md` (or relative `../../design/v0.x/…` from `roadmaps/v0.x/` milestones).
- Do not recreate a flat `doc/design/00-*.md` set alongside version trees.
- New capability still needs a roadmap phase before code (see `.cursor/rules/doc-versioning.mdc`).
