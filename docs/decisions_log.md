# Decisions Log

Design pivots and architectural decisions for mrinspect. Append a new numbered
entry whenever a non-trivial design choice is made, reversed, or revisited.

**Entry format**

```
## N. <short title>

**Date:** YYYY-MM-DD
**Status:** current | superseded by #M | reverted

**Initial idea**
What we first considered or shipped.

**Why it was wrong**
The constraint, incident, or insight that forced a change.

**Current approach**
What the code does now.

**Lesson**
The general principle worth remembering — the part that survives even if the
specific decision is later reversed.
```

**Rules**

- Keep entries narrative, not prescriptive — "rules" belong in `AGENTS.md`.
- One entry per decision, even small ones. Easier to link to later.
- Never edit a past entry's content; if a decision is reversed, add a new entry
  and mark the old one `superseded by #N`.
- Code is authoritative. If an entry contradicts current code, the entry is
  stale — supersede it rather than rewriting history.

---

<!-- Append entries below, starting from #1. -->
