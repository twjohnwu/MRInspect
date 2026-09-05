# Margherita Pizza API Conventions

These conventions keep ordering endpoints consistent across menu, checkout, and delivery operations. They apply to synchronous APIs and asynchronous response adapters owned by the service.

## Response envelope

Successful responses place the primary resource under a named field and include a schema revision. Metadata stays separate so adding tracing information cannot change the resource shape.

## Correlation identifiers

Every inbound request receives a correlation identifier if the caller did not provide one. The same value follows downstream calls and appears in structured operational events.

## Request identifiers

A request identifier names one transport attempt, while a correlation identifier may span retries. Error responses return both when available so support can distinguish duplicate attempts.

## Version negotiation

Clients request a supported contract version through a dedicated header. Unsupported versions fail before business processing and list only the versions still accepting new traffic.

## Content types

JSON endpoints require an explicit media type and reject ambiguous form bodies. Download endpoints set a concrete type and filename rather than relying on client sniffing.

## JSON field names

Wire fields use lower camel case and stable domain terms. Renaming an internal struct does not authorize changing a published field name.

## Monetary amounts

Amounts contain an integer minor-unit value and a three-letter currency code. The server rejects floating-point prices and mixed currencies within one basket.

## Timestamp fields

Timestamps use a complete offset and include seconds. Date-only kitchen schedules use a separate local-date field so midnight conversion is never implied.

## Pagination cursors

List endpoints use opaque cursors tied to the current sort order. A cursor from another filter set is rejected instead of producing an unstable continuation.

## Filter parameters

Filters have one documented type and comparison rule. Repeated scalar filters are invalid, while list filters preserve the server-defined union semantics.

## Sort ordering

Every pageable endpoint defines a deterministic secondary order by resource identifier. Unknown sort fields fail validation rather than reverting to an undocumented default.

## Error codes

Machine-readable error codes remain stable across wording changes. Each code documents whether the caller may retry, correct input, or contact an operator.

## Validation details

Validation errors identify the field path and violated constraint. They do not echo full payment tokens, delivery notes, or other sensitive input.

## Authentication context

Handlers consume a verified customer or kitchen identity from middleware. Business code must not parse credentials or infer identity from an order identifier.

## Rate limit responses

Rate-limited responses include the limit category and a retry delay. The delay is advisory, and clients still apply bounded jitter before another attempt.

## Idempotency headers

Mutation endpoints document whether an idempotency key is required. The key scope includes customer identity and operation name so unrelated actions cannot collide.

## Webhook signatures

Outbound kitchen webhooks sign the exact transmitted bytes with a rotating key reference. Receivers can accept the current and immediately previous references during rotation.

## Webhook retries

Webhook retries preserve the event identifier and body revision. Delivery attempts stop after the configured age even if the numerical retry budget remains.

## Earliest response marker

The review response cleaner scans every accepted marker and cuts at the EARLIEST marker occurrence by string position, not by marker list priority. If `### MR Info` appears before `## Code Review`, `cleanResponse` keeps content from `### MR Info` so the real review body is not discarded.

## Quoted marker isolation

A marker quoted later inside a reviewed diff must not hijack response cleaning merely because it has higher list priority. Regression cases include a tail-quoted `## Code Review` after the real `## Review`, plus an echoed marker before content that downstream validation must still gate.

## Status mapping

Transport status codes map from stable domain outcomes in one shared table. A handler must not choose a different code for the same inventory or payment condition.

## Batch requests

Batch endpoints cap item count and validate every item before starting work. Results retain input order and give each item its own success or error envelope.

## Partial responses

Callers may request optional expansions such as kitchen details. Failure to load an optional expansion is represented explicitly and never removes the primary order resource.

## Localization fields

The API returns message keys and structured arguments rather than server-localized prose for business errors. Customer applications choose language without parsing an English sentence.

## Compatibility policy

Adding an optional response field is compatible, while changing meaning or type requires a new contract version. Clients must tolerate unknown fields but may rely on documented enum values.

## Deprecation headers

Deprecated operations return their retirement date and replacement operation name. The header appears throughout the announced window, including on validation failures.

## Contract examples

Examples use fictional customers, kitchens, and order identifiers. Every example is checked for field names and types during documentation tests.
