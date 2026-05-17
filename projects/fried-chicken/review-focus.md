# Fried Chicken Review Focus

## Critical Areas

### Temperature Safety
- Oil temperature must stay between 160°C and 180°C. Flag any hardcoded temperatures outside this range.
- `frying-controller` must never proceed if temperature sensor returns an error — fail-safe, not fail-open.
- Brine temperature must be validated ≤ 4°C before `chicken.brined` is published (cold-chain compliance).

### Kafka Consumer Reliability
- Consumers must only commit offsets after successful MongoDB write. Flag any early `Commit()` calls.
- Each consumer must handle `context.Canceled` gracefully — no panics on shutdown.
- Dead-letter topics (`{topic}.dlq`) must be used for messages that fail after 3 retries.

### MongoDB Write Safety
- All writes to `pipeline_batches` must use `w: majority` write concern.
- Upserts must include a `updatedAt` timestamp. Flag missing timestamp updates.

## Common Bugs to Watch For

- Temperature unit confusion (°F stored where °C is expected).
- Kafka offset committed before MongoDB write confirmed (message loss on crash).
- Missing DLQ routing for failed consumer messages.
