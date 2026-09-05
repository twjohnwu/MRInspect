# Shared Concurrency Standards

These standards apply when service code performs work in parallel or shares mutable state. Designs should make ownership and shutdown behavior visible at the call site.

## State ownership

Every mutable value has one documented owner or one synchronization mechanism. Passing a pointer across workers transfers responsibility only when the sending worker stops using it.

## Immutable handoff

Prefer immutable messages between concurrent components. If copying is expensive, use a single owner and send commands that request mutations.

## Mutex scope

A mutex protects a named invariant rather than an arbitrary group of lines. Keep slow I/O and callbacks outside the critical section unless consistency explicitly requires otherwise.

## Lock ordering

Code that may acquire multiple locks documents a global acquisition order. Helpers must not hide a reverse order that can deadlock only under uncommon traffic.

## Concurrent metrics verification

A shared metrics struct and its slices must be guarded by a mutex for every read and write, then deep-copied before JSON serialization after releasing the lock. The `Makefile` must run `go test -race ./...`, and a many-goroutine regression test must verify final counts so the race detector observes real concurrent logging access.

## Condition waiting

Wait loops recheck their predicate after every wakeup. A notification is a hint that state may have changed, not proof that the requested condition now holds.

## Channel ownership

The sender that owns a channel is responsible for closing it. Receivers never close a shared input because another producer may still be active.

## Queue capacity

In-memory channels and work queues have explicit finite capacity. The overflow path blocks, rejects, or sheds work according to a documented service policy.

## Cancellation propagation

Child operations derive cancellation from the request or worker lifecycle. A detached operation needs its own bounded context and a documented reason for outliving its parent.

## Deadline budgeting

Reserve time for response encoding and cleanup when allocating downstream deadlines. Passing the entire remaining deadline to the first dependency leaves no room for recovery.

## Goroutine lifetimes

Every goroutine has a clear exit condition and an owner that waits for it. Library functions do not start background loops that callers cannot stop.

## Worker pools

Use bounded worker pools for variable streams of independent tasks. Worker count follows resource constraints such as connection budgets rather than incoming backlog alone.

## Backpressure signals

Producers must observe when consumers cannot keep pace. Backpressure should propagate to admission control instead of becoming hidden memory growth.

## Atomic values

Atomic operations are appropriate for isolated counters or immutable pointer swaps. Multi-field invariants require a lock or single owner because separate atomic writes do not form a transaction.

## Duplicate suppression

Concurrent requests for the same cache key may share one in-flight load. Cancellation by one waiter must not invalidate the result for remaining waiters.

## Shared maps

A map accessed by multiple goroutines needs synchronization even when most access is read-only. Publishing a fully built immutable map through one synchronized swap is a valid alternative.

## Shared slices

Appending may replace a slice backing array while readers still hold an older header. Establish ownership or synchronize the entire access pattern rather than protecting only the append statement.

## Cache refresh

Refresh builds a replacement value away from the read path and publishes it atomically. Failed refresh keeps the last valid value until its hard expiry policy applies.

## Transaction boundaries

Do not hold process locks while waiting for a database transaction. Prefer database constraints and short transactions for invariants shared across instances.

## File writers

Multiple workers send file records to one writer rather than seeking independently. The writer reports flush failures to the lifecycle owner before shutdown completes.

## Graceful shutdown

Shutdown stops producers, drains bounded queues, waits for workers, and closes sinks in that order. A deadline caps the whole sequence and leaves durable work replayable.

## Error collection

Parallel branches return errors through a bounded aggregation path. Preserve the first causal error and summarize additional failures without blocking a worker forever.

## Result ordering

When output order is contractual, attach an input sequence number and reorder after workers finish. Do not rely on scheduler completion order remaining stable.

## Fan out limits

Fan-out over tenants, files, or regions uses a semaphore or pool. Launching one goroutine per untrusted input can exhaust memory before backpressure begins.

## Timer lifecycle

Timers and tickers are stopped when their owner exits. Reused timers follow the drain and reset rules of their implementation to avoid stale wakeups.

## Once initialization

One-time initialization is reserved for immutable process-wide data. Initialization that can fail or needs refresh belongs behind an explicit lifecycle component.

## Review evidence

Concurrency changes include a test that forces overlapping operations and asserts a stable invariant. Reviewers also inspect ownership, cancellation, and the behavior when one branch fails.
