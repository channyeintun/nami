---
name: go-style-guide
description: Guidance for Go coding tasks covering package design, errors, concurrency, style, tooling, and current Go 1.27 conventions.
keywords: golang, go, go style, go conventions, idiomatic go, errors.AsType, strings.CutLast, encoding/json/v2, generic methods, go error handling, go concurrency, go packages, go tooling
argument-hint: Use for Go or Golang coding tasks, style decisions, refactors, naming, API design, error handling, concurrency, or modernization to current Go idioms.
---
Apply when task is Go or Golang code.

Principles:
- Obvious over clever. Prefer direct control flow and small focused functions.
- Design packages, not scripts. Keep core logic independent of CLI, transport, storage, and UI.
- Reach for the standard library first.
- Handle errors explicitly and close to the failure point.

Package design:
- `cmd/` for thin binaries. `internal/` for module-private app code. `pkg/` only for intentionally reusable APIs.
- Give each package one responsibility and keep dependency directions simple.
- Accept interfaces where behavior is consumed, not where values are produced.
- Avoid pointers to interfaces.
- Choose receiver kinds deliberately. Pointer receivers need pointers or addressable values.

API and naming:
- Export the smallest API surface that solves the problem.
- Prefer concrete return types unless callers benefit from an interface.
- Verify interface compliance at compile time when a type must satisfy a public contract.
- Use names that describe behavior, not implementation details.
- Avoid package-name stutter and built-in names.
- Use `ErrName` or `errName` for sentinel errors and the `Error` suffix for custom error types.

Errors:
- Use `errors.New` for stable sentinel errors and `errors.Is` for matching.
- Use custom error types when callers need structured details.
- Prefer `errors.AsType[T]` in Go 1.26+ for concrete typed error extraction.
- Use `errors.As` when matching a non-error interface or when the older pointer-target form is needed.
- Use `%w` only when exposing the wrapped error is part of the contract.
- Keep wrapping context short: `read config`, `dial upstream`, `decode payload`.
- Handle each error once. Do not log an error and then return the same error unchanged.
- Avoid panics in production paths. Return errors and let callers decide.
- Call `os.Exit` or `log.Fatal` only from `main` or the outermost command boundary.

Concurrency and shared state:
- Every goroutine needs ownership, a stop condition or stop signal, and a way to wait for exit.
- Do not fire-and-forget goroutines in library code.
- Do not start background goroutines in `init()`.
- Choose channel capacity deliberately based on coordination, backpressure, and expected burstiness.
- Keep `sync.Mutex` and `sync.RWMutex` as value fields and do not embed mutexes.
- Prefer typed atomics from `sync/atomic` in new code. Stay consistent inside packages already using another wrapper.

Data ownership and initialization:
- Copy slices and maps at package boundaries when later mutation would be surprising or unsafe.
- Prefer nil slices for no data in internal APIs. Return empty slices when an external contract, wire format, or caller expectation makes that shape clearer.
- Check emptiness with `len(s) == 0`, not `s == nil`.
- Prefer `var items []T` when building slices incrementally from the zero value.
- Use `make(map[K]V)` for empty programmatic maps and literals for fixed initial contents.
- Use field names in struct literals when the type is declared elsewhere.
- Omit zero-value struct fields unless they add meaning.
- Use `var state T` for zero-value structs and `&T{}` instead of `new(T)` for struct pointers.
- Use `new(expr)` in Go 1.26+ to take a pointer to any value, replacing hand-written `ptr[T any](T) *T` helpers.
- Use `defer` to clean up files, locks, tickers, spans, and similar resources unless a proven hot path justifies avoiding it.
- Avoid `init()` for non-trivial setup. Keep any unavoidable `init()` deterministic and free of I/O or hidden environment dependencies.

Control flow and style:
- Prefer early returns and reduce nesting.
- Remove unnecessary `else` blocks after `return`, `break`, or `continue`.
- Keep variable scope as small as practical. Use `if err := ...; err != nil {}` when it helps clarity.
- Use `:=` for obvious local values. Use `var` for zero values, explicit types, or top-level declarations.
- Group related imports, constants, vars, and types.
- Keep two import groups by default: standard library first, everything else second.
- Avoid naked boolean arguments when the meaning is unclear.
- Use the comma-ok form for type assertions unless failure is impossible by construction.
- If you embed a type, put embedded fields first and do not embed purely for convenience or if it leaks internals or breaks zero values.
- Add field tags for marshaled structs when the encoded form must be explicit, stable, renamed, or non-default.
- Keep reusable `Printf` format strings as `const`, and give custom `Printf`-style helpers a trailing `f` so `go vet` can validate them.

Time, performance, and modern Go:
- Use `time.Time` for instants and `time.Duration` for durations.
- If an external contract cannot carry `time.Duration`, include the unit in the field name.
- If an external contract cannot carry `time.Time`, default to RFC 3339 strings unless it already defines another format.
- Use `strconv` over `fmt` in hot conversion paths.
- Add map and slice capacity hints when the size is known or easy to estimate and the benefit is real enough to keep the code clear.
- Avoid repeated string or byte-slice conversions in hot paths.
- Prefer standard library helpers such as `slices`, `maps`, and `cmp` when they make code simpler.
- Use `slices.Backward` for reverse iteration instead of a manual descending index loop.
- Use `strings.CutLast` and `bytes.CutLast` in Go 1.27+ instead of `LastIndex` followed by manual slicing.
- Use the `uuid` package in Go 1.27+ instead of formatting random bytes into a UUID shape by hand.
- In Go 1.27+, `encoding/json` is backed by `encoding/json/v2`. Stay on v1 unless you need the v2 API;
  reach for `encoding/json/v2` and `encoding/json/jsontext` for option-driven or token-level processing.
- Methods may declare their own type parameters in Go 1.27+. Use a generic method when the operation
  belongs to a type's namespace, but note interface methods still cannot be generic.
- Keep code compatible with the module's declared Go version even when a newer feature exists.

API patterns and tooling:
- Prefer plain constructors for small APIs.
- Use option structs or functional options when optional configuration is growing, but do not hide required inputs inside optional config.
- Format with `gofmt` or `goimports`.
- Run `go vet` and `staticcheck` regularly.
- Use `revive` when the repo wants style linting and `golangci-lint` when one runner is useful.
- Do not add `golint` to new workflows.
- When upgrading Go versions, run `go fix ./...` and review the modernizers.