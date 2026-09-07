---
source: stdd-plan skill references/23-design-patterns.md
date: 2026-09-07
tags: [coding_standard, design-patterns, stdd]
---

# GoF 23 Design Patterns — distilled catalogue

Bundled reference template for `stdd-plan` S-43. This is the **default**
template only until a project's first `stdd-plan` run — after that, the
project's own `wiki/coding_standard/` copy is canonical and this bundled
copy is not read again for that project.

Apply the S-43 rule strictly: introduce a pattern only when a repeated
structure or a predictable point of variation is already visible in the
design. A pattern introduced without that justification is over-design.

## Creational

- **Factory Method** — subclasses decide which concrete class to
  instantiate; apply when a creation decision needs to vary by subclass.
- **Abstract Factory** — families of related objects created together;
  apply when a system must stay agnostic to a concrete product family.
- **Builder** — construct a complex object step by step; apply when a
  constructor would otherwise need many optional parameters.
- **Prototype** — clone existing objects instead of building from scratch;
  apply when object creation is expensive relative to copying.
- **Singleton** — exactly one instance, globally accessible; apply only for
  genuinely global shared state (e.g. a config registry) — used sparingly.

## Structural

- **Adapter** — reconcile an incompatible interface with what callers
  expect; apply at the boundary of a third-party/legacy integration.
- **Bridge** — decouple an abstraction from its implementation so both can
  vary independently; apply when both sides are expected to grow.
- **Composite** — treat individual objects and compositions uniformly;
  apply to recursive tree-shaped data (e.g. UI components, file trees).
- **Decorator** — attach responsibilities to an object dynamically; apply
  when subclassing for every combination of behavior would explode.
- **Facade** — a simplified entry point over a complex subsystem; apply
  when callers only need a narrow slice of a large subsystem's surface.
- **Flyweight** — share state across many fine-grained objects; apply when
  memory footprint from many similar objects becomes a real constraint.
- **Proxy** — a stand-in controlling access to another object; apply for
  lazy loading, access control, or remote-call indirection.

## Behavioral

- **Chain of Responsibility** — pass a request along a handler chain; apply
  when more than one handler might process a request and the set can grow.
- **Command** — encapsulate a request as an object; apply when
  undo/redo, queuing, or logging of operations is required.
- **Interpreter** — represent a grammar and interpret sentences in it;
  apply only for genuinely small, stable domain-specific languages.
- **Iterator** — sequential access without exposing underlying
  representation; apply whenever a custom collection type is introduced.
- **Mediator** — centralize complex communication between objects; apply
  when objects reference each other in a tangled many-to-many way.
- **Memento** — capture and restore an object's internal state; apply for
  undo/rollback of state without breaking encapsulation.
- **Observer** — one-to-many notification of state changes; apply when
  multiple parts of a system must react to the same event.
- **State** — alter behavior when internal state changes; apply when a
  class's conditional logic already branches heavily on a status field.
- **Strategy** — swap an algorithm's implementation at runtime; apply when
  more than one interchangeable variant of a behavior already exists.
- **Template Method** — fix an algorithm's skeleton, defer steps to
  subclasses; apply when several classes share the same overall sequence
  but differ in specific steps.
- **Visitor** — separate an algorithm from the object structure it
  operates on; apply when new operations are added more often than new
  node types.

Source: distilled from a public "23 design patterns" summary article
(tracked with full citation in the STDD framework's cross-cutting reference
table); this file only carries the distilled name-and-applicability list,
not the original prose.
