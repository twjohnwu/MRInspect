# Margherita Pizza Testing Guide

Tests describe the expected behavior of the fictional ordering backend at several boundaries. Suites favor deterministic inputs and failures that identify the violated business rule.

## Test layers

Unit tests cover pure decisions, component tests cover storage adapters, and journey tests cover a small ordering flow. Each behavior is assigned to the lowest layer that can prove it without duplicating broader setup.

## Unit boundaries

Units are chosen around pricing, scheduling, and state transition decisions. External clocks and repositories enter through narrow interfaces so the decision can run without network or disk access.

## Table driven cases

Tables group cases that share setup and vary only meaningful inputs and outcomes. Case names state the business condition, such as closed kitchen or expired reservation.

## Failure paths

Every remote operation has tests for timeout, explicit rejection, and malformed success data. Assertions verify both the returned error category and whether durable state was changed.

## Fixed clocks

Time-sensitive tests inject a fixed clock at a named instant. Advancing time is explicit so quotation expiry and dough readiness do not depend on test execution speed.

## Random seeds

Property and allocation tests record their random seed. A failing seed can be replayed directly, and normal unit cases use fixed identifiers whenever randomness adds no value.

## Fixture builders

Builders start from a minimal valid order and expose focused options for the case under test. They reject contradictory options rather than creating an impossible fixture silently.

## Golden documents

Golden documents are used only where the full serialized contract matters. Reviewers inspect intentional updates, and volatile timestamps are normalized before comparison.

## Handler contracts

Handler tests send encoded requests through routing and validation middleware. They assert status, response schema, and safe error details without binding to internal call order.

## Database adapters

Adapter tests run against an isolated temporary database with real constraints. Each test owns its schema namespace and closes every transaction before cleanup.

## Migration forward checks

Migration tests apply every schema step from an empty database. They then start the current repository layer to confirm new constraints and indexes are usable.

## Migration compatibility checks

Compatibility cases read rows written by the previous application shape. A deployment must tolerate that shape while old instances may still be draining.

## Queue consumers

Consumer tests cover redelivery, duplicate messages, and exhausted retry policy. Acknowledgement occurs only after the expected durable effect is asserted.

## Property checks

Pricing properties verify totals never become negative and component sums remain consistent. Schedule properties verify an accepted preparation window stays within kitchen opening hours.

## Fuzz inputs

Fuzzers target menu decoders, cursor parsers, and delivery-note normalization. Seeds include empty, oversized, truncated, and unusual Unicode input while preserving bounded resource use.

## Race detector execution

The repository `Makefile` test target runs `go test -race ./...` so concurrent metrics changes are exercised by the race detector in routine test runs. A metrics test launches many goroutines that call logging methods, waits for completion, and checks the final API-call and error counts.

## Parallel isolation

Parallel tests may not share process-global configuration or mutable fixture directories. Temporary resources are scoped to the test and environment changes use automatic restoration.

## Scheduler stress

Scheduler stress cases interleave order cancellation with kitchen acceptance. Assertions focus on valid terminal states rather than assuming a particular goroutine execution order.

## Capacity simulations

Capacity simulations feed bounded meal-time bursts into the worker model. They verify admission decisions and queue-age calculations without asserting timing at millisecond precision.

## Long running checks

Long-running checks repeatedly rotate menus and reconcile inventory in a controlled loop. They are separated from the fast suite and publish their seed and iteration count on failure.

## Ordering journeys

Journey tests create a basket, quote it, authorize payment, and dispatch to a kitchen simulator. Each journey uses fictional customer data and verifies the externally visible order history.

## Kitchen simulator

The kitchen simulator supports acceptance, capacity rejection, and delayed acknowledgement. Tests configure behavior declaratively instead of sleeping or reaching into simulator internals.

## Payment fake

The payment fake records operation keys and can return known, declined, or unknown outcomes. Reconciliation tests control the eventual lookup result independently of the initial response.

## Snapshot stability

User-facing receipt snapshots normalize generated identifiers but retain prices and toppings. A field-order change alone should not hide a semantic difference in the receipt.

## Assertion messages

Assertions name the field, expected value, and observed value. Helpers mark themselves so a failure points to the scenario rather than the assertion implementation.

## Cleanup guarantees

Cleanup functions are registered immediately after a resource is acquired. Cleanup is idempotent because setup failures may trigger only part of the normal lifecycle.

## Flaky test handling

A flaky test is quarantined only with an owner and a recorded reproduction hypothesis. Retries may collect evidence temporarily but never redefine intermittent failure as success.
