## Go Coding Philosophy

- **Write obvious code, not clever code.** Prioritize readability over cleverness. If someone has to think hard to understand it, rewrite it.

## Error Handling

- Always check errors explicitly. Never swallow or hide failures.
- Clear error handling = reliable tools.
- Use `errors.AsType` [v1.26] instead of `errors.As`

## Standard Library

- Target Go 1.27. Prefer stdlib over hand-rolled equivalents.
- Use `strings.CutLast` / `bytes.CutLast` [v1.27] instead of `LastIndex` plus manual slicing.
- Use the `uuid` package [v1.27] instead of formatting random bytes into UUID shapes.
- Use `slices.Backward` for reverse iteration and the `atomic.Int64`/`atomic.Uint64` types
  instead of the `atomic.AddT(&x, n)` functions.
- Run `go fix ./...` after a toolchain bump; it applies the current modernizers.

## Composability

- Split logic into small, focused functions grouped into packages.
- Build for reuse: tools should be composable into other tools, APIs, or larger systems.

## Vulnerability Checks

- Run `govulncheck` for dependency and code vulnerability scanning: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
- Run `npm audit` in Node package directories to scan npm dependencies for known vulnerabilities.
