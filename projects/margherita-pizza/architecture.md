# Margherita Pizza System Architecture

## Services

| Service | Responsibility |
|---------|---------------|
| `dough-service` | Manages dough fermentation timers, hydration ratios, and batch scheduling |
| `tomato-sauce-api` | Processes and stores San Marzano tomato batches; exposes sauce readiness status |
| `mozzarella-service` | Tracks fresh mozzarella inventory, handles cold-chain temperature alerts |

## Data Flow

1. `dough-service` polls `tomato-sauce-api` to confirm sauce is ready before releasing dough batches.
2. `mozzarella-service` publishes inventory events via Redis Pub/Sub consumed by the assembly coordinator.
3. All three services write audit logs to a shared PostgreSQL `ingredient_events` table.

## Key Conventions

- Use PostgreSQL advisory locks when updating batch status to avoid race conditions.
- All gRPC endpoints must include deadline propagation (`ctx` with timeout).
- Redis keys follow the pattern `{service}:{entity}:{id}` — e.g., `dough:batch:abc123`.
- Database migrations live in `migrations/` and use sequential integer naming (`001_init.sql`).
