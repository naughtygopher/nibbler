# 0006. Panic errors

- Status: Accepted
- Date: 2026-11-07

## Context

A panicking processor is recovered and turned into an error, but a non-error panic value became `fmt.Errorf("%+v", rec)` and an error panic value was passed through as is. Callers could not tell a crash from a regular failure, e.g. to alert on crashes while only logging timeouts.

`ProcessorErr` was called without any recovery. A panic in the error handler killed the listener goroutine and, being unrecovered, the whole application. This contradicts the README's claim that nibbler is resilient to panics.

## Decision

```go
var ErrPanic = errors.New("processor panicked")
```

1. A recovered processor panic is wrapped with `ErrPanic`. If the panic value is an error, it is wrapped too (`%w: %w`), so both `errors.Is(err, ErrPanic)` and `errors.Is(err, value)` hold. Otherwise it is formatted with `%v`
2. `ProcessorErr` runs under its own `recover`. Its panic is discarded, since there is no other place to report it, and the listener carries on as if the handler returned normally

## Consequences

- `errors.Is(err, ErrPanic)` identifies crashes in `ProcessorErr`, and in `Err()` / `Listen`'s return when `ResumeAfterErr` is disabled
- Existing `errors.Is(err, panicValue)` checks keep working
- The error message changes from `<value>` to `processor panicked: <value>`, which only matters to users matching on error strings
- A bug in `ProcessorErr` can go unnoticed. Users who need visibility should recover and log inside their handler

## Alternatives considered

- A `PanicError` type carrying `debug.Stack()`: more API surface and multi-KiB error strings. Users who need stack traces can capture them inside their processor
- Re-panic from `ProcessorErr`: crashes the application, against nibbler's resilience goal
- Leave panic values unwrapped: keeps crashes indistinguishable from returned errors
