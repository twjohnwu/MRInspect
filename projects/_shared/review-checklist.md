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

## Final decision

Summarize blocking findings separately from optional follow-up. Approval means the stated requirements and safety conditions have direct evidence in the reviewed change.
