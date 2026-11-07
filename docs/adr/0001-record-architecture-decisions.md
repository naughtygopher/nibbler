# 0001. Record architecture decisions

- Status: Accepted
- Date: 2026-11-07

## Context

Nibbler v0.3.0 changes its API, lifecycle and flush semantics across several releases worth of decisions. Commit messages and PR descriptions explain _what_ changed, but the _why_ and the rejected alternatives get lost within a few months.

## Decision

Every architectural decision is recorded as an ADR in `docs/adr/`, one file per decision, named `NNNN-kebab-title.md`. Each ADR has the sections: Context, Decision, Consequences and Alternatives considered. [`README.md`](README.md) in this directory is the index.

An ADR is merged in the same PR which implements it. Its status starts as `Proposed` and is set to `Accepted` or `Rejected` before merge. A decision which is later replaced is marked `Superseded by NNNN`, it is never deleted.

## Consequences

- Reviewers get the reasoning alongside the code
- Users can trace any behaviour change back to a single document
- Every non-trivial change needs a short write-up, which is the intended friction

## Alternatives considered

- Single `DECISIONS.md` file: grows into an unreviewable wall of text, and diffs mix unrelated decisions
- PR descriptions only: not versioned with the code and not discoverable from the repository
