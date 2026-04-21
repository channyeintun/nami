## Go Coding Philosophy

- **Write obvious code, not clever code.** Prioritize readability over cleverness. If someone has to think hard to understand it, rewrite it.
- Prefer simple `if` statements over complex abstractions. Use clear, descriptive names (`readFile`, not `rf` or something fancy).
- Avoid over-engineering. Boring and clear beats clever and opaque.

## Package & Structure Design

- Design packages, not just programs. Keep core logic independent of I/O.
- Follow this structure convention:
  - `/cmd/toolname` → CLI entry point
  - `/internal/...` → core logic
  - `/pkg/...` → reusable components
- Each function does one job. Each package owns one responsibility.

## Error Handling

- Always check errors explicitly. Never swallow or hide failures.
- Return meaningful error messages. Ask "what can go wrong here?" and handle it at the point of failure.
- Clear error handling = reliable tools.
- Use `errors.AsType` [v1.26] instead of `errors.As`

## Composability

- Split logic into small, focused functions grouped into packages.
- Build for reuse: tools should be composable into other tools, APIs, or larger systems.