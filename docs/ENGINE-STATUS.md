# rawhttp engine status

This file records what was run against the `rawhttp-hardening` branch and
what is still open. It is not a certification. A blank cell means that
measurement was not taken.

The properties these tests check are in
[SECURITY-GUARANTEES.md](SECURITY-GUARANTEES.md). Defaults are in
[SECURITY-BASELINE.md](SECURITY-BASELINE.md).

Host for local runs: Windows/amd64, Intel Core i5-6200U, Go 1.26.9
(`GOTOOLCHAIN=go1.26.9`). This machine has no gcc, so `-race` cannot be
built here (`go: -race requires cgo`). Race jobs are the Ubuntu CI
workflows in `.github/workflows/tests.yml`.

## How the claim grows

Each row is a condition. The engine's documented properties become stronger
only when the evidence in the right-hand column exists. None of these is a
claim of completeness.

| Evidence | Status |
|----------|--------|
| Uninterrupted long fuzz of every target, with crashers added to the corpus | pending (weekly CI, one hour per target; not waited on for this branch) |
| Differential tests against `net/http` with no unexplained accept/reject split | done locally: `TestDifferentialAgainstNetHTTP` PASS; every split is rawhttp-stricter or the documented `OPTIONS *` case |
| The smuggling corpus: every `Reject` case answered 4xx and the connection closed; the follow-up request not served | done locally: `TestSmugglingCorpus` and `TestSmugglingCorpusSplitReads` PASS (89 cases: 77 reject, 12 accept) |
| Independent human review by a party that did not write the code | pending (no reviewer engaged) |

## Unit and integration (this branch)

| Run | Result |
|-----|--------|
| `go test ./... -skip '^(TestGate_\|TestAllocs_)'` (no race) | PASS. `github.com/zatrano/rawhttp` 65.8s; `differential` 5.7s; `tests/smuggling` 3.1s. `TestAllocs_*` skipped: `testing.Benchmark` tries `b.N` near 1e9 and OOMs this host. `TestGate_*` lives in the separate `bench/` module. |
| `go test ./... -race` | not run locally (no cgo). CI job `race` on ubuntu-latest. |
| `go test -gcflags=all=-d=checkptr -skip '^(TestGate_\|TestAllocs_)' ./...` | PASS (61.98s / 2.98s / 1.56s). Combined `-race -gcflags=all=-d=checkptr` is CI job `checkptr`. |
| `go test ./tests/smuggling ./differential` | PASS |
| Goroutine leak tests (`TestNoGoroutineLeakAfterShutdown`, `TestNoGoroutineLeakAfterClientDisconnects`) | PASS. The disconnect test waits for `OpenConnections==0` before declaring a leak (Windows keep-alive teardown can still be inside the +2 goroutine slack). |
| TLS defaults (`TestServerTLSConfigRefusesOldVersionsAndWeakCiphers`) | PASS (part of the package run) |
| `unsafe` allowlist (`TestUnsafeOnlyInAllowlistedFiles`) | PASS |
| `TestLingerDefaultCapBoundsFiveThousand` | skipped on Windows; Linux CI job `fs-linger-race` runs it under `-race` five times |

## Fuzz (this session)

Each target is run for at most ten minutes locally. The weekly workflow
(`.github/workflows/weekly-fuzz.yml`) runs one hour per target after the
pull request is open; that run is not waited on.

| Target | Local duration | Executions | Findings | Corpus added |
|--------|----------------|------------|----------|--------------|
| `FuzzServeConn` | 10 min | 1,364,528 | none (PASS) | 89 smuggling seeds; 65 new interesting kept in the fuzz cache only |
| `FuzzRequestLine` | 10 min | 589,854 | none (PASS) | seeds only |
| `FuzzHeaders` | 10 min | 1,321,312 | none (PASS) | seeds only |
| `FuzzChunkedBody` | 10 min | 997,125 | none (PASS) | seeds only |
| `FuzzClientResponse` | 10 min | 4,883 | none (PASS) | seeds only |
| `FuzzUpgradePredicate` | 10 min | 2,326,767 | none (PASS) | seeds only |
| `FuzzMultipart` | 10 min | 1,346,136 | none (PASS) | seeds only |
| `FuzzDifferentialReadRequest` | 10 min | 1,492,739 | none (PASS) | seeds only |
| `FuzzDifferentialNetHTTP` | 10 min | 2,040,871 | none (PASS) | 89 smuggling seeds |
| `FuzzDifferentialFraming` | 10 min | 3,168,207 | none (PASS) | 89 smuggling seeds |

No crasher was produced. The persistent corpus committed under
`testdata/fuzz` is the 89 smuggling cases (three targets). Interesting
inputs discovered during the ten-minute runs stayed in the local fuzz
cache and were not crashers, so they were not copied into the corpus.

Long fuzz / CI: weekly job is scheduled (`cron: 0 2 * * 0`) and
`workflow_dispatch`. After the draft PR is open it is scheduled, not
started until Sunday 02:00 UTC unless dispatched. Per-PR short fuzz is
the `fuzz` job in `tests.yml` (8–15 s per target). Status of the
one-hour runs: not started (scheduled).

## Differential vs `net/http`

`TestDifferentialAgainstNetHTTP` compared every smuggling case plus
`extraCases()`. Verdict `disagree` (both accept, different sequences)
failed the test; count is 0. Verdict `stricter` (rawhttp served fewer
requests than `net/http`) is logged, not failed.

| Kind | Count | Notes |
|------|------:|-------|
| rawhttp reject, net/http accept | 35 | listed below; intended |
| both accept, different sequences | 0 | |
| documented, kept | 1 | `extra/7` `OPTIONS *`: rawhttp serves the handler; Go `net/http` in this harness does not (RFC 9112 asterisk-form) |

Stricter cases (rawhttp 0 requests, net/http 1):
`host-port-not-numeric`, `host-two-ports`, `upgrade-not-allowed`,
`te-on-get`, `host-percent`, `content-type-duplicate`,
`expect-garbage-suffix`, `te-cl`, `cl-duplicate-equal`,
`proxy-connection`, `connect`, `cl-te`, `host-comma`,
`authorization-duplicate`, `origin-duplicate`, `expect-list-unknown`,
`bare-lf-request`, `expect-on-get`, `te-obs-fold`, `te-header-hop`,
`host-port-too-long`, `te-cl-zero`, `te-http10`,
`chunk-trailer-transfer-encoding`, `chunk-trailer-content-length`,
`absolute-form`, `chunk-trailer-obs-fold`, `dotdot`, `cl-on-get`,
`host-ipv6-unclosed`, `scheme-relative`, `trace`, `cl-leading-zero`,
`version-1.2`, `obs-fold`.

## Smuggling corpus

| Result | Value |
|--------|-------|
| Cases | 89 (`tests/smuggling`) |
| Rejected and connection closed | 77 Reject cases: 4xx, `Connection: close`, handler not run, follow-up request not served |
| Follow-up request served after a reject | 0 |
| Accepted streams | 12, including `pipelined-two` (response count scans `HTTP/1.1 ` anywhere in the concatenated keep-alive wire) |

## Review

AI-assisted adversarial review of this branch is recorded in the
framework repository at `docs/REVIEW-LOG.md` (grok-4.7 and composer-2.5;
GPT/Claude/Gemini quota was exhausted). Independent human review is
pending.
