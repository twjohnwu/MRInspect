# Margherita Pizza Configuration

This guide defines how the pizza-ordering backend receives operational settings. Configuration changes should remain understandable without reading the deployment scripts.

## Configuration ownership

Each setting has one owning service and one documented fallback behavior. A service must reject settings owned by another component instead of silently interpreting them.

## Layer order

Compiled defaults are applied before environment-specific files and process values. Later layers may override earlier ones, and the resolved source is recorded in startup diagnostics.

## Environment names

The accepted environment names are local, test, staging, and production. Unknown names stop startup so an accidental spelling cannot select a partial profile.

## Required values

Payment signing mode and order database name are required in every deployed profile. The loader reports all missing keys together so an operator can repair one release attempt.

## Optional values

Optional values document both their default and the reason that default is safe. An empty string is treated separately from an omitted value whenever blank text has business meaning.

## Typed parsing

Durations, byte sizes, and percentages are parsed into typed values at the boundary. Parsing errors name the setting and supplied shape without printing sensitive content.

## Secret references

Configuration stores secret references rather than secret material. The runtime resolves each reference into memory and excludes the resolved value from logs and snapshots.

## Development profile

The development profile uses a single kitchen and short dough timers. It still enables the same validation rules as production so local behavior does not hide malformed orders.

## Test profile

The test profile fixes clocks, random seeds, and delivery zones. Fixtures may override a value within one test process but must restore isolation for parallel cases.

## Staging profile

The staging profile uses synthetic customer identities and non-settling payment flows. Menu and inventory shapes match production closely enough to exercise migrations before release.

## Production profile

The production profile requires explicit capacity limits for every active kitchen. Startup fails if a kitchen references a menu revision that has not been published.

## Feature switches

Feature switches control reversible behavior, not permanent product variants. Each switch records an owner, expiry date, and behavior for both enabled and disabled states.

## Rollout percentages

Percentage rollouts hash a stable order identifier into fixed buckets. Changing the percentage expands or contracts the same cohort instead of reshuffling every customer.

## Kitchen regions

Region records define local time zones, currencies, and preparation calendars. A kitchen may serve only regions declared in its own configuration block.

## Store catalogs

Catalog configuration maps a kitchen to an immutable menu revision. Editors publish a new revision rather than modifying the version already attached to accepted orders.

## Payment policies

Payment policy selects authorization timing and refund windows by order channel. Monetary thresholds use integer minor units to avoid decimal parsing differences.

## Delivery radii

Delivery radii are expressed in whole meters around a kitchen service area. Polygon exclusions take precedence over the broad radius for bridges and inaccessible streets.

## Tax tables

Tax tables are versioned by effective date and region. An accepted order retains the table version used at quotation time even if rates change before baking.

## Menu schedules

Menu schedules use the kitchen's local clock and explicit day boundaries. Overnight availability is represented as two intervals so comparisons never wrap implicitly.

## Lane overlay replacement

The Margherita Pizza project owns a per-project `lanes.yaml` overlay for review configuration. An overlay entry replaces the canonical lane with the same `id` in place, while a new lane id appends after canonical lanes; this is a config-only change and does not merge individual fields.

## Resource selectors

Every review lane declares resource selectors as explicit `sets` and `tags` lists. The `spec-conformance` overlay pins `sets: [margherita-pizza-docs]` with empty tags so another system's docs cannot enter this project's retrieval scope.

## TopK zero-value default

An omitted or zero-value `topK` in lane configuration resolves to the explicit `DefaultLaneTopK` constant of 8. A negative value follows the same default path, ensuring the retriever never receives `TopK <= 0` and silently returns nothing.

## Schema evolution

New configuration fields begin as optional values with documented defaults. A later release may make them required only after every deployed profile contains the field.

## Deprecation window

Deprecated keys emit a warning for two release cycles before removal. During that window, specifying both old and new keys is an error because precedence would be unclear.

## Startup validation

Validation runs after all layers are combined and before listeners open. Cross-field rules check that delivery windows, kitchen capacity, and menu schedules form a usable plan.

## Diagnostics

Startup diagnostics show normalized non-secret values and their source layer. Lists are sorted for stable diffs, while user-declared priority lists preserve their configured order.

## Change approval

Configuration changes include a rollback note and an owner who can observe the rollout. High-impact capacity or payment changes also identify the metric that triggers reversal.
