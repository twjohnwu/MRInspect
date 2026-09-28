# Shared Review Checklist

This checklist gives reviewers a consistent way to inspect changes across fictional services. Apply only the items relevant to the changed behavior and record concrete evidence for concerns.

## Change intent

Confirm the change description states the observable problem and intended outcome. The implementation should solve that problem without introducing unrelated behavior.

## Scope boundary

List the components, data, and callers that are meant to change. Flag edits outside that boundary when they lack a clear dependency on the stated intent.

## Requirements trace

Connect each normative requirement to code, configuration, or a verification step. A requirement is not satisfied merely because nearby code was exercised.

## API compatibility

Check published request and response fields for changed type, meaning, or required status. Additive fields still need documentation and clients must be allowed to ignore them.

## Data model changes

Inspect defaults, nullability, constraints, and old rows for every schema change. Application code must tolerate the states possible during a rolling deployment.

## Migration safety

Migrations should be bounded, restartable where practical, and compatible with the previous application version. Large backfills run separately from schema locks and report progress.

## Transaction semantics

Verify that state transitions and their durable side effects share the intended transaction boundary. Error paths must roll back or leave an explicitly recoverable state.

## Error propagation

Errors retain causal context and a stable category as they cross layers. Logging an error does not replace returning it when the caller controls recovery.

## Security boundaries

Treat every external request and stored untrusted value as hostile at its boundary. Validation, authorization, and output encoding belong close to the operation they protect.

## Secret handling

Secrets must not enter logs, test snapshots, command arguments, or generated reports. Review redaction on both success and failure paths because errors often include raw input.

## Authorization checks

Authorization uses the verified actor and requested action, not possession of a resource identifier. Bulk operations enforce the same decision for every selected item.

## Privacy exposure

Return and record only fields required for the current purpose. New analytics dimensions receive the same privacy review as new persisted columns.

## Input validation

Boundary validation covers size, type, format, and cross-field invariants. Rejected input should not trigger partial writes or expensive downstream operations.

## Output stability

Deterministic output orders collections and normalizes optional values consistently. Stable output reduces noisy diffs while preserving all contractually meaningful data.

## Earliest marker review

When cleaning generated review text, select the EARLIEST occurrence across all response markers instead of stopping at marker list priority. Include a regression where a higher-priority marker is quoted near the tail so quoted diff text cannot hijack the cut and discard the real review body.

## Resource lifetime

Files, rows, timers, and response bodies are closed on every return path. Ownership should be apparent immediately after acquisition rather than deferred to distant cleanup code.

## Lane overlay review

A per-project `lanes.yaml` overlay replaces a canonical lane by matching `id`, and a new id appends in declaration order. Review config-only changes to ensure resource selectors use the intended `sets` and `tags` and cannot pull another system's documentation into retrieval.

## TopK default review

An omitted, zero, or negative `topK` must resolve to an explicit positive `DefaultLaneTopK` before retrieval. Review both configuration loading and hand-constructed lane paths so `TopK <= 0` cannot cause a silent no-op with zero chunks.

## Testing evidence

Tests should fail for the original defect and pass because of the behavioral change. Assertions verify outcomes and durable effects rather than private implementation order.

## Failure injection

Exercise dependency timeout, malformed data, and partial completion where those states are credible. Verify that cleanup and retry decisions remain bounded under each injected failure.

## Capacity bounds

Collections, queues, request bodies, and fan-out all require explicit limits. The overflow behavior should protect core work and remain visible to operators.

## Operational signals

New failure modes have stable logs or metrics that identify impact without sensitive values. Alerts should describe a user-visible symptom and link it to an owned response action.

## Rollout plan

Risky behavior changes use a staged rollout with a named observer and stop condition. Compatibility must hold while old and new instances operate together.

## Rollback plan

Confirm rollback does not require data that the new version has already discarded. If rollback is unsafe after migration, the forward-recovery procedure must be explicit.

## Documentation sync

Update contracts, operational procedures, and examples in the same change as behavior. Documentation should describe current guarantees without promotional or conclusive quality language.

## Dependency review

New dependencies need a narrow purpose, maintained version policy, and failure behavior. Prefer existing platform capabilities when they meet the requirement without expanding runtime trust.

## Oven window review

Confirm any overnight bake window declares two explicit, non-wrapping intervals in `OvernightSplit` rather than relying on an implicit end-before-start wrap. Reviewers should trace `ValidateBakeWindow` to prove a wrapped single interval is rejected, not silently accepted as crossing midnight.

## Basket currency review

Confirm every path that appends an item to a basket, including a promotional or credit item added outside `addItem`, is routed through the shared currency-consistency check. A rejected item must not remain in the basket afterward.

## Notice board retention review

Confirm a posted notice carries a positive TTL and that reading the board sweeps entries whose TTL has elapsed. A board that only appends and never sweeps will grow without bound and keep showing stale notices.

## Metric name stability review

Confirm a metric name is a fixed constant rather than built by concatenating a mutable label such as a region into the name string. A varying attribute belongs on a dimension, and reviewers should check that renaming that attribute cannot silently rename the metric.

## Timeout budget review

Confirm downstream calls each receive a bounded share of the remaining request deadline rather than the full remaining deadline passed through unchanged from the previous call. A reserved encoding buffer should be subtracted before the remaining time is divided.

## Fixture option review

Confirm a fixture builder rejects combining an explicit empty-state option with an item option instead of building a fixture where the reported empty flag and stored items disagree. Reviewers should trace the first contradictory option to a builder error naming both options.

## Service boundary review

Confirm a service reads another service's state only through that service's own HTTP client or API, never by opening a direct database connection to its private table. A schema change to that private table must not silently break the caller's readiness check with no compile-time signal. The two services under a data-flow arrow in the architecture doc must exchange information the same way for every property they depend on, not just some.

## Routing region ownership review

Confirm a kitchen's routing rules are validated against that same kitchen's own declared region list, and that the check is owned by whoever maintains kitchen configuration rather than a team with no visibility into routing rules. An undeclared region on a routing rule should fail startup, not be silently routed by a neighboring kitchen's configuration.

## Pagination tie-break review

Confirm a pageable listing's cursor encodes a secondary identifier and that the page query compares the full pair, not just the primary sort field. Rows that share the same primary sort value must still resolve through a stable, deterministic secondary key.

## Retry trace linkage review

Confirm a retried operation attaches its attempt as a distinct span under the request's existing trace root instead of starting an unrelated new root per attempt. Reviewers should trace context propagation from the first attempt through every retry to the same root identifier.

## Fry temperature safety review

Confirm an out-of-range fry stage temperature returns a validation error instead of being clamped into the safety band. Reviewers should trace `ValidateFrySchedule` to prove a typo'd temperature surfaces as an error, not a silently adjusted value that later drives the wrong duration.

## Batch queue backpressure review

Confirm the pending batch queue returns a queue-full error once its fixed capacity is reached rather than growing without bound. Reviewers should trace `PendingCount` and `ErrQueueFull` to prove backpressure reaches the producer instead of hiding as unbounded memory growth.

## Marinade cold-chain review

Confirm brine temperature is checked against the cold-chain limit inside config validation before the brined event is allowed to publish. Reviewers should trace that the check runs before publish, not as an after-the-fact audit.

## Pickup listing tie-break review

Confirm a pickup listing's cursor encodes the order ID alongside the primary pickup time and that the page query compares the full pair. Orders sharing an identical pickup time must still resolve through a stable, deterministic secondary key.

## Oil-change alert reliability review

Confirm a consumer commits its Kafka offset only after the corresponding database write has succeeded. Reviewers should trace commit ordering relative to the write to prove a crash between the two leaves the message redeliverable instead of silently dropped.

## Coating thickness sample count review

Confirm a coating schedule's declared dip count matches the number of recorded thickness samples before the schedule is accepted. Reviewers should trace that a schedule missing a sample for one of its declared dips is rejected, not recorded as complete.

## DLQ retry exhaustion review

Confirm a consumer routes a message to its dead-letter topic once a fixed retry count is reached, instead of retrying the same message forever and blocking everything queued behind it. Reviewers should trace the retry counter through to the DLQ publish call.

## Consumer shutdown signal review

Confirm a consume loop distinguishes a cancellation error raised during normal shutdown from a genuine client error, and exits cleanly on the former instead of panicking. Reviewers should trace every fetch error path to prove shutdown never reaches the panic branch.

## Upsert timestamp freshness review

Confirm every upsert to a shared collection sets its own updated-at timestamp on the write, regardless of what the caller's field map already contains. Reviewers should trace that a staleness alert can distinguish a fresh write from an old one.

## Write concern acknowledgment review

Confirm a collection that must survive a primary failover is opened with an explicit majority write concern rather than inheriting the client's default. Reviewers should trace the collection's write concern setting back to its constructor.

## Final decision

Summarize blocking findings separately from optional follow-up. Approval means the stated requirements and safety conditions have direct evidence in the reviewed change.
