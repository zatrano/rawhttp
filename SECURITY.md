# Security

## Supported versions

| Version | Supported |
|---------|-----------|
| 0.2.x   | yes (GA for application embedding) |
| 0.1.x   | superseded |

Report issues via GitHub. Do not open public issues for unfixed zero-days without coordinated disclosure.

## Status (GA readiness)

RawHTTP **v0.2.2** is **Generally Available** as the HTTP/1.1 transport for application servers (ZATRANO V3 and similar embedders). Tag remains on the **0.2.x** line: the public Go API may still evolve with semver-minor care; there is no separate `v1.0.0` tag required for V3 cutover.

It implements common safety controls (timeouts, body limits, Host requirement, CL/TE conflict rejection, CRLF sanitization on response headers, HEAD body suppression, Expect: 100-continue).

It is **not** claimed to be:

- A full RFC 9112 conformance suite
- Immune to every HTTP request smuggling technique
- Suitable as a public-facing reverse proxy without review

## Guarantees (honest)

Prior wording called v0.2 “experimental.” That label is retired for **application embedding**. The “not claimed” list above still applies.
## Verifiable controls

These are repository checks, not third-party certification claims:

1. **Timeouts and limits** — default read/write/idle timeouts, concurrency cap, header/body size caps
2. **Parser refusal matrix** — CL/TE conflict, duplicate Host/CL, absolute-form, Upgrade-by-default 400 (see checklist below)
3. **net/http differential tests** — `test/` corpus comparing accept/reject decisions where applicable
4. **Fuzz targets** — nightly fuzz workflow for request parsing / upgrade surfaces
5. **Race and poison CI jobs** — `-race` where available; `rawhttp_poison` build-tag jobs for slice lifetime

## Mitigations present

1. Default read/write/idle timeouts
2. Default concurrency cap
3. Max header bytes, header count, and body sizes
4. Integer overflow guards on Content-Length and chunk sizes
5. Reject duplicate Content-Length (identical or differing) and leading-zero CL
6. Reject chunked + Content-Length together; reject non-chunked TE
7. Require Host on HTTP/1.1; reject duplicate Host
8. Reject NUL, obs-fold, and whitespace in header names
9. `SetHeader` / `SetContentType` block CR/LF / CTL; client Method/Host/URI similarly validated
10. Malformed requests answered with 400 (unsupported Expect → 417)
11. Only HTTP/1.0 and HTTP/1.1; reject absolute-form and scheme-relative (`//`) targets; `Ctx.Redirect` also rejects scheme-relative Location; `SameSite=None` forces `Secure`
12. Reject CTL in header values; reject `Connection: upgrade` / `Upgrade` (**400** before handler by default). Opt-in `Server.AllowUpgrade` admits a tightly validated WebSocket handshake to the handler for `Hijack` only (see [docs/hijacking.md](docs/hijacking.md)); Origin / CSWSH checks remain the application's responsibility.
13. Limit chunk-extension length; reject chunk-size trailing garbage; validate trailers
14. Reject smuggling-sensitive trailers (Host/CL/TE/Connection/Upgrade/Trailer)
15. Reject TRACE and CONNECT methods
16. `ErrorCallback` for protocol/client errors
17. Asterisk-form only for OPTIONS; reject `#` / `%00` / `%2e` in request-targets
18. TE value must be exactly `chunked`; strip trailing OWS on header values
19. Cap chunk-size hex width; `SetHeader` requires token header names
20. Require CRLF (reject bare LF); reject Host with `@` `/` `\` `,` (len ≤255)
21. Oversized header block → 431; limit chunk frames via `MaxChunks`
22. Reject `..` and `%2e` path segments; reject `TE`; reject Expect:100-continue on GET/HEAD
23. Immediate `Close` in addition to graceful `Shutdown`
24. Reject `Proxy-Connection`; cap header-name length
25. Clamp invalid status codes; omit bodies for 1xx/205
26. Optional `DisablePipelining`; client HEAD ignores body; client trailer count capped; max 5 informational 1xx
27. Length-safe header-name matchers (no panic on short names)
28. Request-line covered by `ReadHeaderTimeout` after first byte (slowloris)
29. Optional `AllowedMethods` / `AllowedHosts` / `MaxURILength` / `RequireTLS` / `GetOnly`
30. `TrustedProxies` + `ClientIP`: X-Forwarded-For / X-Real-IP only when peer is in the allowlist (default = never trust)
31. Optional `MaxConnsPerIP` (429) and `ContinueHandler` (reject Expect: 100-continue → 417)
32. Optional `HeaderReceived` per-request body/timeout overrides; `SecureHeadersMiddleware` (CSP / Permissions-Policy / COOP)
33. `AllowedHosts` matches FQDN trailing dots; `BasicAuthMiddleware` (constant-time) / `RequestIDMiddleware`
34. `Server.ReadBufferSize` independent of `MaxHeaderBytes` ceiling; FS `RejectSymlinks` / `HideDotFiles`

## Smuggling / desync checklist

Status key: **Mitigated** (corpus + parser rule) · **Partial** (common forms rejected; exotic variants open) · **Out of scope**

| # | Vector | Status | Notes / test |
|---|--------|--------|--------------|
| 1 | CL.TE (both CL and TE: chunked) | Mitigated | Reject; `TestSecurityAttackCorpus` |
| 2 | TE.CL (order reversed) | Mitigated | Same conflict rule |
| 3 | Duplicate differing `Content-Length` | Mitigated | Reject |
| 3b | Duplicate identical `Content-Length` | Mitigated | Reject (proxy parity) |
| 4 | Duplicate `Transfer-Encoding` | Mitigated | Reject |
| 5 | Obfuscated TE (`identity`, `gzip`, `chunked, identity`) | Mitigated | TE must be exactly `chunked` |
| 6 | Leading-zero / signed / hex CL | Mitigated | Decimal, no leading zeros |
| 7 | CL / chunk-size integer overflow | Mitigated | Overflow guards |
| 8 | HTTP/1.1 missing / duplicate / non-ASCII Host | Mitigated | Required; unique; ASCII-only |
| 9 | Absolute-form / scheme-relative target | Mitigated | 400 |
| 10 | Obs-fold / bare LF | Mitigated | CRLF required |
| 11 | Header name space / CTL / overlong | Mitigated | Token + length caps |
| 12 | Header value CTL | Mitigated | Reject |
| 13 | Chunk extension overlong / CTL / trailing junk | Mitigated | Cap + validate; reject non-`;` after size |
| 14 | Smuggling-sensitive trailers | Mitigated | Host/CL/TE/Connection/… |
| 15 | Too many chunk frames | Mitigated | `MaxChunks` |
| 16 | GET/HEAD with body / Expect:100 | Mitigated | Reject |
| 17 | `Connection: upgrade` / `Upgrade` | Mitigated (default) | **400** before handler when `AllowUpgrade` is false; with `AllowUpgrade`, only a standards-shaped WS handshake reaches the handler |
| 18 | TRACE / CONNECT | Mitigated | Reject |
| 19 | Path `..` / `%2e` / `%00` / `#` / `\` / CTL | Mitigated | Reject literal and encoded dots |
| 20 | Response splitting via `SetHeader` | Mitigated | CR/LF/CTL blocked |
| 21 | Header bomb (count / size) | Mitigated | `MaxHeaders` / `MaxHeaderBytes` → 431 |
| 22 | Pipelined request desync | Partial | Keep-alive pipelining OK by default; `DisablePipelining` closes on leftover |
| 23 | HTTP/2 downgrade / H2c preface | Out of scope | HTTP/1.1 only |
| 24 | Front-end ↔ RawHTTP parser disagreement | Partial | Prefer TLS + reviewed reverse proxy; `%2e`, identical CL, ASCII Host |
| 25 | Client response CL/TE confusion | Mitigated | Rejects CL+TE, duplicate TE, non-chunked TE, duplicate CL; forbidden trailers |

Re-run the living corpus after parser changes:

```bash
cd test && go test -count=1 -run '^TestSecurity' -v
cd test && go test -fuzz=FuzzServeConn -fuzztime=30s -run=^$
```

## Stricter than `net/http` (differential notes)

On a same-input differential corpus (RawHTTP `ServeConn` vs `net/http` server), RawHTTP was **stricter** (reject / non-200 while `net/http` accepted) for cases including:

- `Content-Length` + `Transfer-Encoding: chunked` together
- Obs-fold header continuation
- Bare LF (non-CRLF) request framing
- Absolute-form request targets
- Empty `Host`
- `%2e` path segments
- `Upgrade` / `Connection: upgrade` (RawHTTP **400** by default; `net/http` may still invoke the handler). With `AllowUpgrade`, RawHTTP is still stricter on malformed WS handshakes (POST, HTTP/1.0, `h2c`, multi-value Upgrade, duplicate Key, body present).
- `Expect: 100-continue` with a known `Content-Length` above the effective body cap: RawHTTP answers **413** and does **not** write `100 Continue`. `net/http` sends `100 Continue` when the handler first reads the body, then a `MaxBytesReader` in the handler can fail mid-body. Chunked `Expect` (no length yet) still gets `100`, and the cap is applied while reading.
- `RequestConfig.RejectStatus` (400–599) writes a fixed response and closes without reading the body or calling the handler. `net/http` has no equivalent pre-handler reject; a handler always starts, and unread bodies are consumed for keep-alive. A per-request `StreamBody` that the handler does not finish also closes the connection instead of draining it.

No corpus case in that comparison run showed RawHTTP **looser** than `net/http` on the compared accept/reject decision. Prefer a reviewed reverse proxy when front-end and RawHTTP parsers must agree under attack traffic.

## Known gaps / operator guidance

- Prefer TLS termination and a reverse proxy (or mature peer) when facing untrusted clients until the parser has more field use
- Set explicit `MaxRequestBodySize` for your threat model
- Do not put untrusted data into response headers without validation (library already rejects CR/LF/CTL)
- Absolute-form / CONNECT are rejected; **upgrade / WebSocket handshakes are rejected** (400) unless `Server.AllowUpgrade` is explicitly enabled — then only a validated handshake reaches `Hijack`
- Reject GET/HEAD with a body; reject CTL/`\` in request-target; reject duplicate TE
- `ReadHeaderTimeout`, connection/request counters, `Ctx.IsTLS`
- Run short fuzz in CI; longer local fuzz before releases (`-fuzztime=5m`+)

## Testing

```bash
cd test && go test -count=1 -skip '^(TestGate_|TestAllocs_)' ./...
cd test && go test -count=1 -run '^TestSecurity' -v
cd test && go test -fuzz=FuzzServeConn -fuzztime=30s -run=^$
cd test && go test -race -count=1 -skip '^(TestGate_|TestAllocs_)' ./...
cd test && go test -race -tags rawhttp_poison -count=1 -skip '^(TestGate_|TestAllocs_)' ./...
cd test && go test -run '^TestGate_' -count=1 -v
go run ./scripts/plaintextbench -c 256 -d 10s
gofmt -l .
go vet ./... && (cd test && go vet ./...)
staticcheck ./...
govulncheck ./...
```

GitHub Actions runs on every push/PR:

1. **unit + security** — all `test/` tests except long comparison gates
2. **race** — race detector (`test/`), plus a second job under `-tags rawhttp_poison`
3. **coverage** — `rawhttp` package ≥70% statement coverage
4. **style** — gofmt, go vet, staticcheck, golangci-lint
5. **vuln** — govulncheck
6. **build** — go build + go mod tidy
7. **gate** — ServeConn rival floors + never-slower trimmed rounds + 0-alloc hello (see `docs/performance.md`)
8. **fuzz** — short ServeConn / request-line / headers / chunked / differential ReadRequest runs
9. **nightly fuzz** — longer fuzz via schedule / `workflow_dispatch`
`TestSecurityAttackCorpus` attempts request smuggling, Host/path abuse,
chunk/trailer attacks, response splitting, and header bombs — RawHTTP must reject them
without invoking the handler.
