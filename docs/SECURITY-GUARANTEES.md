# rawhttp security guarantees

This file lists the properties rawhttp is built to hold, the tests that check
each one, and where each one stops. A property is only as strong as its tests:
a test shows the property for the inputs it covers, not for every input.

Results of the latest run are in [ENGINE-STATUS.md](ENGINE-STATUS.md).

## R1: ambiguous framing is rejected and the connection is closed

A request whose length two HTTP/1.1 implementations could read differently is
answered with a 4xx and `Connection: close`, the handler is not called, and no
byte after it is parsed as a request.

Rejected (RFC 9112 §6, §7 and RFC 9110 §8.6):

- `Content-Length` together with `Transfer-Encoding`, in either order
- more than one `Content-Length` line, identical values included
- `Content-Length` with a sign, leading zero, list, hex, whitespace inside or overflow
- any `Transfer-Encoding` other than exactly `chunked` (case-insensitive), any
  repeated `Transfer-Encoding` line, and `Transfer-Encoding` in HTTP/1.0
- chunk sizes with junk, sign, overflow, more than 16 hex digits or a bare LF
- chunk extensions with CTL bytes or over the length cap
- trailers named `Content-Length`, `Transfer-Encoding`, `Host`, `Connection`,
  `Upgrade` or `Trailer`
- bare LF or bare CR anywhere in the header block, obs-fold, whitespace before
  the colon, a header line without a colon
- missing, empty, duplicate or malformed `Host` on HTTP/1.1
- absolute-form and scheme-relative targets, `CONNECT`, `TRACE`, HTTP versions
  other than 1.0 and 1.1

Write side: a handler cannot set `Content-Length` or `Transfer-Encoding`
(`SetHeader` and `SetHeaderBytes` return `ErrHeaderInvalid`); the engine
writes both from the body it sends. `Connection: close` from a handler closes
the connection; other `Connection` values are ignored. Canned error responses
compute their `Content-Length` from their body.

Tests:

- `tests/smuggling`: `TestSmugglingCorpus`, `TestSmugglingCorpusSplitReads`
  (each case also fed one byte per read). Every reject case is followed by a
  valid request that must not be served.
- `differential`: `TestDifferentialAgainstNetHTTP`, `TestDifferentialMutations`,
  `FuzzDifferentialFraming`. rawhttp may be stricter than `net/http`; a case
  where both serve different request sequences fails.
- `TestSecurityAttackCorpus`, `TestHTTP10TransferEncodingRejected`,
  `TestSetHeaderRejectsFramingHeaders`, `TestSetHeaderConnectionCloseClosesConnection`,
  `TestSetHeaderConnectionKeepAliveIsIgnored`, `TestCannedResponsesAreWellFramed`,
  `TestFixedStatusResponsesAreWellFramed`, `TestSetCookieRejectsAttributeInjection`.

Limits:

- The corpus and mutations cover known smuggling families. A new technique that
  relies on a byte pattern no test produces is not covered until a case is added.
- The differential baseline is `net/http` from the Go toolchain under test.
  Front-end proxies (Nginx, HAProxy, CDNs) are not compared.
- HTTP/2 and h2c are not implemented, so downgrade attacks against an HTTP/2
  front end are out of scope.

## R2: no input causes a panic, an endless loop, or unbounded memory or CPU

Tests:

- Fuzz targets: `FuzzServeConn`, `FuzzRequestLine`, `FuzzHeaders`,
  `FuzzChunkedBody`, `FuzzClientResponse`, `FuzzUpgradePredicate`,
  `FuzzMultipart`, `FuzzDifferentialReadRequest`, `FuzzDifferentialNetHTTP`,
  `FuzzDifferentialFraming`. The persistent corpus in `testdata/fuzz`
  holds every smuggling case (`TestFuzzCorpusCoversSmugglingCases`).
- `TestSmugglingCorpus` runs every case under a 5 s deadline.
- `TestReadTimeoutEndsStalledRequest`, `TestNoGoroutineLeakAfterShutdown`,
  `TestNoGoroutineLeakAfterClientDisconnects`, the `TestLinger*` tests.
- CI runs the package tests with `-race` and with
  `-race -gcflags=all=-d=checkptr`.

Limits:

- Fuzzing searches; it does not prove absence. Run time per target is in
  ENGINE-STATUS.md.
- A handler panic is recovered (unless `DisablePanicRecovery`) and answered with
  a closing 500; the engine does not bound what a handler allocates.
- Memory per connection is bounded by the limits in R3; the total is bounded by
  `Concurrency` times those limits, which can still exceed the host's RAM if the
  operator raises them.

## R3: resource limits are on by default and documented

A zero-value `Server` enforces every limit below. The values are in
[SECURITY-BASELINE.md](SECURITY-BASELINE.md).

Tests: `TestDefaultLimitHeaderBytes`, `TestDefaultLimitHeaderCount`,
`TestDefaultLimitBodySize`, `TestDefaultLimitChunkedBodySize`,
`TestDefaultLimitChunkCount`, `TestDefaultLimitRequestLine`,
`TestDefaultLimitChunkSizeOverflow`, `TestDefaultLimitContentLengthOverflow`,
`TestReadTimeoutEndsStalledRequest`, `TestLingerDefaultCapBoundsFiveThousand`,
`TestMaxPipelineDepthCloses`, `TestDefaultPipelineDepthAllowsShortPipeline`,
`TestDefaultMaxRequestsPerConnStillKeepsAlive`, `TestMinBodyRateClosesSlowBody`,
`TestSlowHeaderHitsReadHeaderTimeout`, `TestSlowBodyHitsReadTimeout`.

Limits:

- `MaxURILength` defaults to zero (no separate cap); the request line is still
  inside the `MaxHeaderBytes` budget.
- `MaxConnsPerIP` defaults to zero (no per-IP cap). One address can hold up to
  `Concurrency` connections.
- `MaxRequestsPerConn` zero means 1_000_000; negative removes the cap.
- `MaxPipelineDepth` zero means 64; negative removes the cap.
- `MinBodyBytesPerSecond` zero means 64; the check starts after one second of
  body I/O so a small body that arrives in one packet is not rejected.
- Limits that the operator sets to zero or negative values fall back to the
  default or disable the limit as documented per field.

## R4: a connection is never reused after a parse error

After any request the engine cannot frame, it writes at most one error
response, discards a bounded amount of input (`LingerDrain`, `LingerTimeout`,
`MaxLingering`) and closes. The same holds after a handler panic, after an
un-hijacked upgrade, and when a streamed request body is left unread.

Tests: `TestStateParseErrorEndsConnection`, `TestPanicClosesConnection`,
`TestStateUpgradeWithoutHijackCloses`, `TestStateUnreadBodyIsNotARequest`,
`TestStateUnreadStreamedBodyIsNotARequest`, `TestStateClientConnectionCloseStops`,
`TestStateMaxRequestsPerConn`, `TestStateDisablePipelining`,
`TestStateExpectContinueRejected`, `TestLingerDropsPipelinedNextRequest`, and
the reject branch of `TestSmugglingCorpus`.

Limits:

- A connection taken with `Hijack` belongs to the handler; rawhttp does not
  parse it again.

## I5: `unsafe` only in allowlisted files

`TestUnsafeOnlyInAllowlistedFiles` parses every Go file in the module and fails
on `//go:linkname`, on an `unsafe` import outside the allowlist, on an
`unsafe` selector that the allowlist does not name, and on a stale entry.

| File | Allowed | Why |
|------|---------|-----|
| `ctx.go` | `unsafe.Pointer` | `sliceOverlaps` compares backing-array addresses; it never builds a pointer or a slice from an address. |
| `deadline_buf_windows_test.go` | `unsafe.Pointer` | Test-only `WSAIoctl` call on Windows. |

## TLS defaults

`ServerTLSConfig` (used by `ListenAndServeTLS`) sets TLS 1.2 as the minimum
and limits TLS 1.2 to ECDHE key exchange with AES-GCM or ChaCha20-Poly1305.
TLS 1.3 suites are chosen by Go and are not configurable.

Test: `TestServerTLSConfigRefusesOldVersionsAndWeakCiphers` (handshake with
TLS 1.1 and with CBC/RSA-only suites fails; a TLS 1.2+ client succeeds).

Limit: a `*tls.Config` the caller builds and passes to `Serve` through
`tls.NewListener` is used as given.

## Not guaranteed

- **Application logic and authorization.** rawhttp frames bytes. It does not
  know who may call a route, whether input is valid for the application, or
  whether a response leaks data.
- **Volumetric DDoS.** Limits bound the cost of each connection. A flood that
  fills the link, the kernel accept queue or `Concurrency` slots is a network
  problem; it needs upstream filtering.
- **Vulnerabilities in dependencies.** rawhttp has no third-party
  dependencies, but it runs on the Go runtime and standard library
  (`crypto/tls`, `net`). Their fixes arrive with the Go toolchain; build with a
  supported patch release.
- **Independent human review.** These properties are checked by the tests in
  this repository and by AI-assisted adversarial review (see the framework's
  `docs/REVIEW-LOG.md`). No independent third party has reviewed the engine;
  that review is pending (see ENGINE-STATUS.md).
- **Parsers in front of rawhttp.** A proxy that reads a request differently
  from rawhttp can still desynchronise its own connection pool. R1 makes
  rawhttp refuse ambiguous input; it cannot make another parser refuse it.
