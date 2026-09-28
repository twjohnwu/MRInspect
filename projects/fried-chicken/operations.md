# Fried Chicken Operations

Operational rules for the crispy coating pipeline: frying safety, queue admission, marinade cold-chain compliance, pickup listing behavior, and oil-health alerting.

## Fry stage temperature safety band

Every fry stage declares a target temperature and duration. `ValidateFrySchedule` rejects any `TargetTempC` outside the 160C to 180C safety band instead of clamping it into range, so an operator typo surfaces as a validation error rather than silently cooking at the wrong duration for a clamped temperature. A documented low-temperature cooldown stage is the only exception, and it must set `AckOverride` explicitly.

## Batch queue admission and backpressure

`frying-controller`'s pending batch queue has a fixed capacity. `Enqueue` returns `ErrQueueFull` once `PendingCount` reaches that capacity instead of appending indefinitely, so a slow fryer produces visible backpressure toward `crispy-coating-api` rather than unbounded memory growth during a rush.

## Marinade cold-chain validation

`MarinadeConfig.Validate` checks `SaltPercent`, `SoakMinutes`, and `BrineTempC` together. A `BrineTempC` above the 4C cold-chain limit fails validation before `brine-service` is allowed to publish `chicken.brined`, so a warm brine tank cannot enter the pipeline.

## Pickup listing pagination tie-break

`ListPickupOrders` encodes its cursor as the pickup ETA paired with the order ID, and the page query compares the full pair. Orders sharing an identical `PickupETA` still resolve through the order ID as a stable secondary key instead of being skipped or repeated across a page boundary.

## Oil-age offset commit ordering

`handleOilAgeEvent` writes the decoded `OilAgeState` to `pipeline_batches` before calling `CommitMessages` on the consumer group. Committing only after a successful write means a crash between the two steps leaves the offset uncommitted and the oil-age sample is redelivered instead of silently lost, keeping the oil-change alert accurate.

## Double-dip coating thickness guard

`ValidateCoatingSchedule` requires `len(ThicknessSamplesMm)` to equal `DipCount` before `crispy-coating-api` accepts the schedule, so a batch declared as double-dipped cannot pass with only a single thickness reading on file. A schedule missing its second sample is rejected rather than recorded as complete.

## DLQ retry exhaustion routing

`handleBatchComplete` tracks `RetryCount` on every failed `batch.complete` message and publishes to the `{topic}.dlq` topic once `maxConsumerRetries` is reached, instead of retrying a permanently malformed message forever and blocking the rest of the `chicken-pipeline` consumer group behind it.

## Consumer shutdown context handling

`ConsumeLoop` checks `errors.Is(err, context.Canceled)` before calling `log.Panic` on a fetch error, so a routine deploy restart of `brine-service` exits the consume loop cleanly instead of crashing the process with a panic stack trace.

## Pipeline batch upsert timestamp

`UpsertBatch` sets `updatedAt` on the `$set` clause of every write to `pipeline_batches`, so a staleness alert can always tell a fresh upsert from one that has not been touched in days, regardless of whether the caller's own field map already carried a timestamp.

## Pipeline batch write concern

`NewBatchCollection` opens `pipeline_batches` with an explicit `writeconcern.Majority()` option instead of the cluster's default `w:1`, so an acknowledged write to the collection survives a primary failover instead of being silently lost.
