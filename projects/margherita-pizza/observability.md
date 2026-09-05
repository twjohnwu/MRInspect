# Margherita Pizza Observability

Observability for the ordering backend connects customer symptoms to kitchen operations without exposing private data. Signals use stable names and bounded dimensions.

## Event vocabulary

Operational events use domain verbs such as order accepted, payment authorized, and pizza dispatched. Each event includes the order revision and emitting component.

## Structured logging

Logs are structured records with a severity, event name, and correlation fields. Free-form prose explains context but is never the only representation of an outcome.

## Sensitive field filtering

Payment tokens, access credentials, and full delivery notes are excluded before records reach a logger. Filtering happens at the logging boundary so every sink receives the same safe shape.

## Customer identifier hashing

Metrics and routine logs use a rotating opaque customer hash where correlation is necessary. Raw customer identifiers remain confined to audited support workflows.

## Order trace roots

Order creation starts a trace root that follows quotation, authorization, and kitchen dispatch. Retries link to the original root while retaining a distinct attempt span.

## Span boundaries

Spans surround remote calls and meaningful queue waits rather than every helper function. Attributes describe the operation category and outcome without copying payload bodies.

## Metric naming

Metric names begin with the service domain and describe a count, duration, or current value. Units appear in the name or metadata and never change after publication.

## Metric dimensions

Allowed dimensions include kitchen region, order channel, and stable outcome code. Order identifiers and arbitrary error strings are forbidden because they create unbounded cardinality.

## Mutex guarded metrics

The logger's mutable `Metrics` struct is shared by concurrent review steps and must be guarded by one `sync.Mutex`. `StartReview`, `LogStep`, `LogAPICall`, `LogError`, and completion snapshot access all lock `metricsMu`, preventing a data race on counters and slices.

## Serialization snapshots

Before JSON serialization or file I/O, completion code locks metrics state and makes a deep-copy snapshot of every mutable slice, then unlocks. Copying only the outer struct aliases slice backing arrays, so concurrent appends could still race while history is serialized.

## Counter semantics

Counters increase for completed facts and never decrease within a process lifetime. A failed attempt and its later successful retry are counted separately with distinct outcomes.

## Duration histograms

Durations use monotonic elapsed time and predefined bucket boundaries. Kitchen preparation and network calls use separate instruments because their useful ranges differ.

## Queue gauges

Queue gauges report ready, delayed, and in-flight work independently. Consumers update values after durable state changes so a process crash cannot imply work was completed.

## Availability indicators

Availability distinguishes valid customer responses from internal transport success. A rejected invalid basket is a healthy response, while an unexplained timeout is not.

## Error categorization

Errors carry a stable category for validation, dependency, capacity, or internal failure. Detailed messages remain in restricted logs and do not become metric labels.

## Dashboard ownership

Every dashboard panel names the signal owner and the question it answers. Panels without an operational decision are removed rather than retained as decorative graphs.

## Alert symptoms

Alerts fire on customer-visible symptoms such as checkout failures or late dispatches. Internal causes appear in diagnostic panels but do not each page the on-call engineer.

## Alert windows

Short and long windows balance fast detection with sustained impact. Both windows use the same success definition so operators do not compare incompatible rates.

## Capacity alerts

Capacity alerts combine queue age with worker saturation. Queue length alone is insufficient because a brief meal-time burst may drain within the promised window.

## Kitchen health

Kitchen health summarizes acceptance lag, preparation backlog, and controller connectivity. Planned closure suppresses traffic but remains visible as a scheduled state.

## Payment health

Payment health separates declined customer instruments from provider errors. Only provider failures consume the dependency error budget for the ordering service.

## Delivery health

Delivery health tracks quote latency, assignment delay, and handoff age. Pickup orders are excluded from delivery denominators through the order channel dimension.

## Audit records

Audit records capture actor, action, target revision, and reason for privileged changes. They are append-only and use a dedicated retention policy separate from debug logs.

## Sampling policy

Successful high-volume traces may be sampled after preserving aggregate metrics. Errors, timeouts, and explicitly investigated order identifiers receive deterministic retention.

## Log retention

Routine application logs expire sooner than financial audit records. Retention classes are assigned by record type, not chosen ad hoc by individual handlers.

## Incident annotations

Deployments, configuration changes, and kitchen closures produce timeline annotations. Operators can correlate a signal change with an event without searching release chat.

## Diagnostic bundles

A diagnostic bundle contains recent safe logs, normalized configuration, and bounded metric snapshots. The bundle excludes secrets and uses relative component names rather than machine paths.
