# Changelog

## v0.2.4 - 2026-10-07

### Fixed

- `Shutdown` no longer sets a read deadline on every tracked connection. It only signals in-progress post-error linger discards. Those discards poll a short read slice and the server stop channel. A hijacked connection keeps the deadlines the application set, and a handler blocked in `Read` is not interrupted. If that handler has not returned when the shutdown context ends, `Shutdown` returns `context.DeadlineExceeded` and leaves the connection open. Idle keep-alive connections are still closed.

### Documentation

- `Shutdown` does not close or re-deadline a hijacked connection. `Close` closes one whose handler is still running; after the handler returns with `KeepHijackedConns`, `Close` leaves it open. Status, install line, and guides say v0.2.4. The README performance tables stay the recorded host-specific snapshot; they were not re-measured.

## v0.2.3 - 2026-10-06

### Fixed

- Evaluate the effective body limit (`Server.MaxRequestBodySize` / `RequestConfig.MaxRequestBodySize`) and `RequestConfig.RejectStatus` before `100 Continue`. A known `Content-Length` over the cap is `413` with `Connection: close` and no `100 Continue`. Chunked `Expect: 100-continue` still receives `100`; the cap is applied while reading.
- After an early error or `RejectStatus` response (`Connection: close`), the connection goroutine half-closes when `CloseWrite` exists (`TCPConn`, `tls.Conn`), discards at most `LingerDrain` bytes (default 256 KiB) for at most `LingerTimeout` (default 1s), then closes. Hitting the byte cap early still waits out the timeout before `Close`: closing with unread TCP data aborts the socket on Windows and drops the response. A client still writing can read `413`/`400`/`431` until that timeout. Hijack and request-body streaming are unchanged. No extra goroutine.
- The post-error discard is capped by `Server.MaxLingering` (default 1024; `0` uses that default; negative is unlimited). The connection goroutine increments `Server.Lingering` on entry and decrements it on the way out, including panic. When the cap is full, or the server is already shutting down, the discard is skipped (no `CloseWrite`) and the connection is closed immediately. `Shutdown` and `Close` interrupt an in-progress wait so a full `LingerTimeout` cannot outlive the server. A lingering connection still holds its concurrency and per-IP slot.
- After headers are released, a chunked body larger than the connection read buffer can be read up to the body cap (`413`) instead of failing as `431`.

### Added

- `RequestConfig.RejectStatus` and `RejectRetryAfter`. A status in 400–599 from `HeaderReceived` writes a short fixed response (standard text for 413/429/503; `Retry-After` when set), does not read the body, does not run the handler, sends `Connection: close`, and does not parse a pipelined next request. Zero keeps the previous behavior.
- `RequestConfig.StreamBody` streams that request via `Ctx.RequestBodyStream`. It does not disable `Server.StreamRequestBody`. When the per-request stream is left unread, the connection closes after the response.
- `Server.MaxLingering` and `Server.Lingering`. `MaxLingering` bounds how many connections may sit in the post-error discard at once. `Lingering` is the atomic count of those connections.

### Documentation

- Promote status to **GA for application embedding** (ZATRANO V3 transport). Tag line remains 0.2.x; reverse-proxy claims unchanged. SECURITY / README / production guide updated.
- SECURITY notes the `100 Continue` and pre-handler reject differences versus `net/http`.

## v0.2.2 - 2026-10-02

### Changed

- Default builds compile `poisonEnabled` to a constant `false` so the compiler DCE removes poison fill paths (`nm` shows no poison symbols without `-tags rawhttp_poison`).
- Replace process labels / wording outside allowlisted docs; SECURITY controls stay verifiable.
- CI unit matrix adds Go **1.22.x** alongside **1.25.x**.

## v0.2.1 - 2026-09-30

### Documentation

- Refresh ServeConn / multibench snapshot tables (2026-09-30 remeasure).
- Align README / SECURITY / guides with v0.2.0 features; RawHTTP display name; English-only docs.
- Stop tracking `.cursor/` IDE rules in the published tree.

## v0.2.0 - 2026-09-30

### Added

- `Server.AllowUpgrade` — opt-in admission of a standards-shaped WebSocket handshake to the handler for `Hijack` (default remains reject-with-400). Connection header is tokenized (Firefox `keep-alive, Upgrade`); `Sec-WebSocket-Key` must base64-decode to 16 bytes.
- `rawhttp_poison` build tag — after Handler, fills pinned request buffer / owned copies with `0xDE` and clears Ctx request slices so accidental retention fails in tests. Default builds use empty stubs (no cost).
- Differential / upgrade property fuzz (`FuzzDifferentialReadRequest`, `FuzzDifferentialNetHTTP`, `FuzzUpgradePredicate`).
- Nightly fuzz workflow (`schedule` + `workflow_dispatch`); CI race job also runs under `-tags rawhttp_poison`.

### Changed

- ServeConn gate floors recalibrated (plaintext ≥2.35×); HostClient timing is informational only.
- `scripts/multibench -strict` is local-only; CI ranking is informational. Blocking gates remain `TestGate_*` (ServeConn).
- `Hijack` clears connection deadlines so the caller owns timeouts.

### Documentation

- README / SECURITY / `docs/hijacking.md` / `docs/performance.md` / `docs/production.md` / `docs/server.md` / `docs/api-reference.md` / `docs/getting-started.md` aligned with Upgrade rejection, AllowUpgrade, poison testing, per-connection memory, hijacked-connection accounting, RawHTTP display name, and v0.2.0.
