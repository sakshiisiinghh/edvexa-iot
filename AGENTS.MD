# Engineering Guidelines

## Code style

- Write simple, idiomatic Go that a teammate can understand quickly.
- Prefer the standard library and existing dependencies.
- Keep functions focused and abstractions justified by actual use cases.
- Avoid unnecessary interfaces, wrappers, helper types, and generic frameworks.
- Use descriptive names without making them excessively long.
- Keep comments concise. Explain non-obvious reasoning and constraints, not what the code already says.
- Do not add comments solely to increase documentation coverage.
- Avoid speculative features and premature optimization.

## Error handling

- Return errors explicitly and wrap them with useful context.
- Validate external input at system boundaries.
- Do not silently ignore errors or add defensive checks for impossible internal states without a reason.

## Architecture

- Keep the existing Go, chi, and PostgreSQL architecture.
- Keep HTTP handlers, business logic, and database operations separated where that separation improves clarity.
- Do not add layers or packages without a concrete benefit.
- Use PostgreSQL constraints and transactions for correctness guarantees that must survive concurrent requests.

## Testing

- Test important behavior, failure cases, and concurrency guarantees.
- Prefer readable tests with clear setup, action, and assertions.
- Do not weaken tests to make an implementation pass.
- Run formatting, build, vet, and relevant tests after changes.

## Scope and workflow

- Implement only the tasks requested in the current phase.
- Review existing code before adding a new abstraction or dependency.
- Show proposed changes before large refactors.
- Keep diffs focused and avoid unrelated cleanup.
- Do not commit unless the approved workflow explicitly calls for it.
- Never claim a test passed unless it was actually run and passed.
