# Shared Coding Standards

## General Principles

- Write clear, self-documenting code. Variable and function names should explain intent.
- Prefer immutability: avoid mutating shared state. Return new values instead.
- Keep functions small and single-purpose (≤ 30 lines is a healthy guideline).
- Never swallow errors silently. Log or propagate every error with context.
- All public APIs must have input validation at the boundary.

## Error Handling

- Return errors to callers; do not hide them in logs only.
- Wrap errors with context: `fmt.Errorf("fetchIngredient: %w", err)`.
- Distinguish between recoverable errors (retry) and unrecoverable ones (fail fast).

## Testing

- Every non-trivial function should have at least one unit test.
- Use table-driven tests for functions with multiple input cases.
- Test the failure path, not just the happy path.
- Mocks are acceptable for external I/O; prefer real implementations for pure logic.

## Security

- Never log secrets, API keys, or credentials.
- Validate and sanitize all external input before use.
- Use parameterized queries for any database interaction.
