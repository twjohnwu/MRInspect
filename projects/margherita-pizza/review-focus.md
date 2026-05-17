# Margherita Pizza Review Focus

## Critical Areas

### Ingredient Freshness Invariants
- Dough batches must not be released if `fermentation_complete_at` is in the future.
- Mozzarella inventory updates must include a `temperature_celsius` field; reject records without it.
- Tomato sauce batches older than 48 hours must be flagged as `stale` — verify any status transitions respect this rule.

### Concurrency & Locking
- Batch status transitions (`pending` → `ready` → `dispatched`) must use PostgreSQL advisory locks.
- Flag any code that updates batch status without a transaction or advisory lock.

### gRPC Deadline Propagation
- Every outbound gRPC call must propagate the incoming context. Flag calls using `context.Background()` instead of the handler's `ctx`.

## Common Bugs to Watch For

- Off-by-one errors in fermentation timer calculations (use `time.Until`, not manual subtraction).
- Missing `defer tx.Rollback()` after `db.Begin()`.
- Redis key typos that deviate from the `{service}:{entity}:{id}` pattern.
