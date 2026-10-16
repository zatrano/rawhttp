<div align="center">

# RawHTTP

Independent HTTP/1.1 engine for Go. Zero external dependencies.

[![Tests](https://github.com/zatrano/rawhttp/actions/workflows/tests.yml/badge.svg)](https://github.com/zatrano/rawhttp/actions/workflows/tests.yml)
[![Static Analysis](https://github.com/zatrano/rawhttp/actions/workflows/static-analysis.yml/badge.svg)](https://github.com/zatrano/rawhttp/actions/workflows/static-analysis.yml)
[![Coding Style](https://github.com/zatrano/rawhttp/actions/workflows/coding-style.yml/badge.svg)](https://github.com/zatrano/rawhttp/actions/workflows/coding-style.yml)
[![Security](https://github.com/zatrano/rawhttp/actions/workflows/security.yml/badge.svg)](https://github.com/zatrano/rawhttp/actions/workflows/security.yml)
[![Performance](https://github.com/zatrano/rawhttp/actions/workflows/performance.yml/badge.svg)](https://github.com/zatrano/rawhttp/actions/workflows/performance.yml)

[![gosec](https://img.shields.io/badge/gosec-enabled-E34C26?logo=go&logoColor=white)](https://github.com/zatrano/rawhttp/actions/workflows/security.yml)
[![govulncheck](https://img.shields.io/badge/govulncheck-enabled-00ADD8?logo=go&logoColor=white)](https://github.com/zatrano/rawhttp/actions/workflows/security.yml)
[![Semgrep](https://img.shields.io/badge/Semgrep-enabled-1B2A4E?logo=semgrep&logoColor=white)](https://github.com/zatrano/rawhttp/actions/workflows/security.yml)
[![Trivy](https://img.shields.io/badge/Trivy-enabled-1904DA?logo=aquasecurity&logoColor=white)](https://github.com/zatrano/rawhttp/actions/workflows/security.yml)

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Version](https://img.shields.io/github/v/tag/zatrano/rawhttp?filter=v*&sort=semver&label=version&color=blue)](https://github.com/zatrano/rawhttp/releases/tag/v1.0.1)
[![Latest Release](https://img.shields.io/github/v/release/zatrano/rawhttp?display_name=tag&label=latest&color=brightgreen)](https://github.com/zatrano/rawhttp/releases/latest)
[![Security Policy](https://img.shields.io/badge/Security-Policy-red?logo=github)](SECURITY.md)

[![Max connections](https://img.shields.io/badge/Max%20connections-262144-0366d6?style=flat-square)](docs/server.md)
[![Deps](https://img.shields.io/badge/Dependencies-0-lightgrey?style=flat-square)](go.mod)

</div>

---

**Status: v1.0.1 (stable API, GA for application embedding).** Breaking API changes need a new major version. Suitable for production application servers (e.g. ZATRANO V3). Not positioned as a reverse proxy. Read [SECURITY.md](SECURITY.md) before public exposure.

```text
Your app / framework
        ↓
     RawHTTP
        ↓
     Network
```

RawHTTP owns listen, connections, HTTP/1.1 parse/write, `Ctx`, limits, and the keep-alive client. Routing and application concerns stay in your code (or your framework).

## Install

```bash
go get github.com/zatrano/rawhttp@v1.0.1
```

Tests sit next to the code they cover; run with: `go test ./...`. Comparison benchmarks and rival gates that import fasthttp live in a separate module (`bench/`) so the root module stays dependency-free.

## Quick start

```go
package main

import (
	"log"

	"github.com/zatrano/rawhttp"
)

func main() {
	log.Fatal(rawhttp.ListenAndServe(":8080", func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("Hello, World!"))
	}))
}
```

Full guides: **[Documentation](docs/getting-started.md)**.

## Features

- HTTP/1.1 server (`Serve` / `ServeConn` / `ListenAndServe` / TLS / reuseport / prefork)
- Keep-alive client (`Client` / `HostClient` / `PipelineClient` / `LBClient`)
- Hot-path request parsing keeps `Method` / `Path` / headers as buffer slices; plaintext hello is **0 allocs/op** (CI gate)
- Protocol hardening (Host, CL/TE, smuggling corpus, fuzz CI) — see [SECURITY.md](SECURITY.md)
- Forms, multipart, cookies, JSON helpers, static files (`FS` / `SendFile`)
- Streaming (request/response), connection `Hijack`, optional `AllowUpgrade` for standards-shaped WebSocket handshakes (no frame codec)
- Middleware helpers (CORS, compress, rate limit, auth, …) — compose by wrapping `Handler`
- Proxy dialers (HTTP CONNECT / SOCKS5), `TCPDialer` + DNS cache

**Not included:** path-parameter router (`:id` / `{id}`), header-name normalization disable API, WebSocket frame codec. By default `Upgrade` / `Connection: upgrade` are **rejected** with 400; set `Server.AllowUpgrade` to admit a standards-shaped handshake to `Hijack` — see [Hijacking](docs/hijacking.md).

## Documentation

| Doc | Topic |
|-----|--------|
| [Getting started](docs/getting-started.md) | Install → first server |
| [Concepts](docs/concepts.md) | Server / Conn / Request / Response / Handler |
| [Server](docs/server.md) | Config, Serve, Shutdown |
| [Request](docs/request.md) / [Response](docs/response.md) | `Ctx` I/O |
| [Headers](docs/headers.md) / [Cookies](docs/cookies.md) / [Body](docs/body.md) | |
| [Routing](docs/routing.md) | Manual routing patterns |
| [Middleware](docs/middleware.md) | Wrap `Handler` |
| [Forms](docs/forms.md) / [Files](docs/files.md) | urlencoded, multipart, FS |
| [Streaming](docs/streaming.md) / [Hijacking](docs/hijacking.md) | |
| [Errors](docs/errors.md) / [Timeouts](docs/timeouts.md) / [Concurrency](docs/concurrency.md) | |
| [Performance](docs/performance.md) | Model + benchmarks |
| [Production](docs/production.md) | Deploy checklist |
| [API reference](docs/api-reference.md) | Exported surface |
| [Examples](docs/examples/) | Hello, JSON, middleware, upload |

## Benchmarks

Plaintext over TCP, rawhttp against fasthttp. Run [38796674394](https://github.com/zatrano/framework/actions/runs/38796674394), commit `ae14757`, 20 rounds, three runs, shared 4 vCPU runner. The server is pinned to CPU 0 with `GOMAXPROCS=1`. The ratio is rawhttp/fasthttp. Each number is the median of the three run summaries. Absolute req/s from this runner is not the result.

**ahead** means the 95% interval sits above 1 and p99 and CPU per request are not higher. It does not mean a 5% lead. 64 and 256 are ahead and under 1.05. One of the three runs at each of those sizes had p99 above 1. 1024 and 4096 are ahead and above 1.05 (1024 runs were 1.128, 1.049, and 1.131). fasthttp is not ahead in any of the four stable cells. JSON, headers, and chunked were not part of this run.

| Conns | Ratio | 95% CI | p99 | CPU | RSS | Result |
|---:|---:|---|---:|---:|---:|---|
| 64 | 1.016 | 1.014–1.019 | 0.985 | 0.966 | 0.977 | ahead |
| 256 | 1.028 | 1.024–1.038 | 0.997 | 0.954 | 0.922 | ahead |
| 1024 | 1.128 | 1.077–1.146 | 0.862 | 0.883 | 0.911 | ahead |
| 4096 | 1.093 | 1.086–1.104 | 0.917 | 0.911 | 0.816 | ahead |

In-process ServeConn checks live in `bench/`. They are a different measurement from the table above. CI floors are in [docs/performance.md](docs/performance.md).

```bash
cd bench && go test -run 'Gate|PublishedLinux' -v
```

## vs net/http / fasthttp / Hertz / gnet

| | RawHTTP | net/http | fasthttp | Hertz | gnet |
|--|---------|----------|----------|-------|------|
| Role | HTTP/1.1 engine | stdlib HTTP | HTTP engine | CloudWeGo HTTP | Event-loop net framework |
| Deps | none | stdlib | compress libs | larger tree | event-loop |
| Router | bring your own | `ServeMux` | bring your own | built-in | N/A (raw) |
| Ctx model | `*Ctx` | `ResponseWriter`+`Request` | `RequestCtx` | `RequestContext` | custom |
| Typical use | engine under apps | general Go | Fiber / custom | microservices | custom protocols |

## Client

```go
c := &rawhttp.Client{}
resp := rawhttp.AcquireResponse()
defer rawhttp.ReleaseResponse(resp)
if err := c.Get("http://127.0.0.1:8080/", resp); err != nil {
	log.Fatal(err)
}
```

## Quality gates (CI)

| Job | What |
|-----|------|
| unit + security | package tests + security corpus |
| race | race detector; also `-tags rawhttp_poison` |
| coverage / style / vuln / build | standard |
| gate | ServeConn rival floors + allocs (`TestGate_*` / `TestAllocs_*`) |
| fuzz | ServeConn, request-line, headers, chunked, differential ReadRequest |
| nightly fuzz | longer fuzz (schedule + `workflow_dispatch`) |

## Used by

[ZATRANO V3](https://github.com/zatrano) uses RawHTTP as its HTTP foundation.

## License

See [LICENSE](LICENSE).
