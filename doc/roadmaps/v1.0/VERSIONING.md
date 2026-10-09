# Versioning

## Product versions

- Pre-release: `0.x` (example: `0.1.0` tags map to milestone **0.1**).
- **0.1** = MVP (usable Create / Prompt / Send / Stream / Wait + built-in tools).
- Minor bumps (`0.2`, `0.3`) add capability slices; breaking public API requires a new major when leaving 0.x, or an explicit design update.

## File naming

| Kind | Pattern | Example |
|------|---------|---------|
| Milestone file | `m_<major.minor>_<slug>.md` | `m_0.1_mvp.md` |
| Milestone directory | `m_<major.minor>/` | `m_0.1/` |
| Phase file | `m_<ver>/p_<ver>.<n>.md` | `m_0.1/p_0.1.1.md` |

- Phase numbers increment within the milestone: `p_0.1.1`, `p_0.1.2`, …
- Slug is lowercase snake or short words: `mvp`, `resume`, `extensibility`.

## Status values

Only these strings:

`planned` | `in_progress` | `done` | `deferred`

## Required sections

**Milestone:** goals, phase list, overall acceptance, non-goals, status  

**Phase:** goals, scope, non-goals, tasks, acceptance, depends-on, linked design chapters, status  

## Rules

1. Do not invent parallel trees (`docs/`, `roadmap/` singular, etc.).
2. No code for a capability until its phase doc exists.
3. Keep `VERSIONING.md` limited to naming/process; put feature design in `doc/design/v1.0/` (current cutoff).
4. This file lives under a cutoff tree (`doc/roadmaps/v1.0/`); do not keep a parallel flat `doc/roadmaps/m_*.md` set.
