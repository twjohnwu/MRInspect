# Project system

How a team defines its own review standards under `projects/`, and how registry, resource sets, and lanes fit together.

[繁體中文版](../tw/project-system.md)

Projects let each team define their own review standards. mrinspect loads the matching project for a service and injects the documentation into the AI prompt as context.

```
projects/
├── registry.yaml               # service-name → system-name mapping
├── resources.yaml              # named resource sets selected by name or tag
├── lanes.yaml                  # canonical ordered lane declarations
├── _lanes/                     # static lane prompt preambles
│   ├── code-diff.tmpl.md
│   ├── spec-conformance.tmpl.md
│   └── standards.tmpl.md
├── _shared/
│   └── coding-standards.md     # standards applied to every system
├── margherita-pizza/           # sample system (Go + PostgreSQL + gRPC)
│   ├── system.yaml
│   ├── lanes.yaml              # per-system lane overlay
│   ├── architecture.md
│   └── review-focus.md
└── fried-chicken/              # sample system (Go + Kafka + MongoDB)
    ├── system.yaml
    ├── lanes.yaml              # per-system lane overlay
    ├── architecture.md
    └── review-focus.md
```

**`projects/registry.yaml`** — maps service names to system directories:

```yaml
defaultSystem: my-system

services:
  payments-api: my-system
  auth-service: my-system
  dashboard: another-system
```

**`projects/my-system/system.yaml`** — describes the system:

```yaml
name: My System
description: >
  Brief description of what this system does and its architecture.
defaultServiceType: backend
frameworks:
  - Go
  - PostgreSQL
serviceTypeOverrides:
  my-dashboard: frontend   # override per-service if needed
```

**`projects/my-system/review-focus.md`** — guides the AI reviewer:

```markdown
# My System Review Focus

## Critical Checks
- All database writes must use transactions
- gRPC calls must propagate the incoming context

## Common Bugs
- Missing `defer tx.Rollback()` after `db.Begin()`
```

**`projects/resources.yaml`** — declares ordered, named resource sets. Lanes select sets by `name` and/or `tags`; every set must choose `mode: retrieval` for indexed chunk retrieval or `mode: full` for whole-document injection, and declares the source `paths` plus optional include/exclude patterns.

**`projects/lanes.yaml`** — declares the ordered multi-review lanes. Each lane requires `id`, `enabled`, `template`, `intent`, and `resources`; selectors may contain explicit `sets` and `tags`. A lane with both selector lists empty receives the diff without external resource documents.

**`projects/_lanes/*.tmpl.md`** — static preambles prepended to their lane prompts. A system can add `projects/<system>/lanes.yaml`; overlay entries merge by lane ID, replacements retain the canonical position, and new IDs append in overlay declaration order.

In multi mode, missing or invalid lane configuration and configurations with no enabled lanes degrade to the single-review path with a named reason in the review. A lane prompt-composition failure is reported as a named lane failure rather than silently replaced by a legacy template.
