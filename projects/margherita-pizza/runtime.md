# Margherita Pizza Runtime Operations

This guide describes the runtime behavior of the fictional ordering service. It focuses on predictable operation from process start through graceful shutdown.

## Process startup

The process validates configuration and database compatibility before accepting traffic. Readiness remains false until menu caches and kitchen routing tables are loaded.

## Process shutdown

Shutdown first stops new order admission and then drains accepted work. A fixed deadline bounds draining, after which unfinished jobs return to the durable queue.

## Readiness checks

Readiness reports whether this instance can safely receive new requests. Optional analytics dependencies do not affect readiness because they are outside the ordering path.

## Liveness checks

Liveness detects a stalled event loop or deadlocked worker pool. It does not query remote dependencies, which could turn an external outage into needless process restarts.

## Order admission

Admission verifies that a selected kitchen is open and below its active-order cap. Rejected requests receive a stable reason code that the client can translate locally.

## Worker pools

Separate bounded pools handle quoting, payment confirmation, and kitchen dispatch. Pool sizes derive from measured service limits and are not scaled from request queue length alone.

## Queue backpressure

Each queue has a hard capacity and a documented overflow response. Producers stop or reject work instead of allocating an unbounded in-memory backlog.

## Request deadlines

Incoming deadlines are propagated through menu, inventory, and payment calls. Internal cleanup may use a short independent context only after the customer request has ended.

## Retry budgets

Retries consume a request-level attempt budget shared across downstream calls. Backoff includes bounded jitter, and permanent validation failures are never retried.

## Idempotency records

Order creation stores an idempotency key with the normalized request digest. Reusing a key with different toppings returns a conflict rather than a previous order.

## Circuit state

Circuit breakers protect optional delivery-quote providers from repeated calls during an outage. Kitchen acceptance remains available for pickup orders when delivery quoting is open-circuited.

## Bulkhead limits

Kitchen dispatch has an independent concurrency limit for each location. A slow oven controller therefore cannot consume every worker allocated to other kitchens.

## Cache warming

Menu revisions used by open kitchens are loaded during startup. Less common historical revisions load on demand and retain a short bounded lifetime.

## Cache invalidation

Publication events invalidate only the affected menu and kitchen keys. Consumers compare revision numbers so a delayed event cannot restore stale data.

## Clock handling

Business calculations receive a clock dependency rather than reading wall time repeatedly. The same instant is used for quotation expiry, kitchen cutoff, and audit records.

## Time zone conversion

Storage uses universal timestamps while kitchen schedules retain an explicit zone. Conversion happens at the scheduling boundary and rejects nonexistent local times.

## Delivery estimation

The estimate combines queue depth, bake duration, handoff time, and route duration. Each component is capped so one corrupted input cannot produce an unusable customer promise.

## Inventory reservation

Ingredient stock is reserved during checkout and released when authorization fails. Reservation expiry is shorter than the payment session to prevent long-lived stock holds.

## Payment authorization

Authorization occurs once the kitchen confirms it can prepare the basket. A stable operation key allows safe reconciliation after a timeout with an unknown remote outcome.

## Kitchen dispatch

Dispatch messages contain the immutable order revision and preparation deadline. Consumers reject older revisions after a topping substitution has been accepted.

## Retriever boundary backstop

Code that constructs a review lane directly must replace a zero or negative `TopK` with `DefaultLaneTopK` before calling `Retrieve`. This boundary backstop prevents the retriever's `TopK <= 0` guard from becoming a silent no-op that returns an empty result.

## Dead-letter handling

Messages that exhaust retry policy move to a quarantine queue with their failure category. Operators replay only after fixing the cause and record the replay batch identifier.

## Graceful degradation

Optional recommendation failures remove suggestions but do not block a valid basket. Core failures in pricing, inventory, or payment remain explicit customer-facing errors.

## Memory limits

Large imports and exports stream records rather than buffering full files. Cache capacities and queue lengths have independent bounds visible in runtime diagnostics.

## Connection pools

Database pool size accounts for all replicas and the server connection budget. Idle connections expire gradually to avoid synchronized reconnect bursts.

## Signal handling

The first termination signal begins graceful shutdown and records its cause. A second signal shortens the remaining drain without skipping final state persistence.

## Recovery startup

After an unclean stop, workers reconcile orders left in transitional states. Recovery uses durable timestamps and operation identifiers instead of assuming the last attempt failed.
