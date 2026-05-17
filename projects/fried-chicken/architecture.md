# Fried Chicken System Architecture

## Services

| Service | Responsibility |
|---------|---------------|
| `brine-service` | Manages brine solution composition and chicken soaking durations |
| `crispy-coating-api` | Tracks seasoned flour batches, coating thickness measurements, and double-dip schedules |
| `frying-controller` | Controls oil temperature, monitors frying duration, and emits `batch.complete` Kafka events |

## Data Flow

1. `brine-service` publishes `chicken.brined` events to Kafka topic `chicken-pipeline`.
2. `crispy-coating-api` consumes `chicken.brined` and emits `chicken.coated` after coating.
3. `frying-controller` consumes `chicken.coated`, manages oil temperature, and publishes `batch.complete`.
4. All state is persisted in MongoDB collection `pipeline_batches`.

## Key Conventions

- Kafka consumer groups follow the pattern `{service}-consumer-group`.
- MongoDB documents use `_id` as UUID strings (not ObjectID) for traceability.
- All temperature values are stored in Celsius as `float64`; conversions to Fahrenheit happen only at API response time.
- Prometheus metrics are exposed on `:9090/metrics` for all services.
