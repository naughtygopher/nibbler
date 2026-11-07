# 0010. Runnable examples as primary docs

- Status: Accepted
- Date: 2026-11-07

## Context

Most exported identifiers had no godoc, so pkg.go.dev showed little beyond signatures. The README example did not survive contact with the code:

- the `Config` snippet declared `trigger trigger`, which does not compile
- it waited for processing with `time.Sleep(time.Second)`
- it read the bank's total balance from `main` while the listener wrote it, a data race

The license badge pointed to another repository's LICENSE.

## Decision

1. Every exported identifier has godoc starting with its name. The package doc covers the 2 triggers, slice ownership, shutdown and `FlushOnStop`
2. `example_test.go` holds runnable examples with `// Output:`, so they are compiled and verified by `go test`:
    - `Example`: the bank statements flow, with `FlushOnStop` and `<-nib.Done()`
    - `ExampleNibbler_Listen`: running and joining the listener within a worker `WaitGroup`
3. The README example mirrors `Example`, with `signal.NotifyContext` in place of a manual cancel
4. The README gains sections for shutdown, benchmarks, migrating from v0.2 and these ADRs, and its `Config` block mirrors the code
5. The license badge points to nibbler's own LICENSE

## Consequences

- The primary usage example cannot silently rot: if it breaks, `go test` fails
- The README `Config` block and benchmark numbers are still copied by hand, and have to be updated along with the code

## Alternatives considered

- Generate the README from the examples: another tool in the build for a single file
- Keep usage only in the README: it already proved to drift from the code
