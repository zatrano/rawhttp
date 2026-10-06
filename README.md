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
[![Version](https://img.shields.io/github/v/tag/zatrano/rawhttp?filter=v*&sort=semver&label=version&color=blue)](https://github.com/zatrano/rawhttp/releases/tag/v0.2.4)
[![Latest Release](https://img.shields.io/github/v/release/zatrano/rawhttp?display_name=tag&label=latest&color=brightgreen)](https://github.com/zatrano/rawhttp/releases/latest)
[![Security Policy](https://img.shields.io/badge/Security-Policy-red?logo=github)](SECURITY.md)

[![Peak RPS](https://img.shields.io/badge/Peak%20RPS-149k-2ea44f?style=flat-square)](#benchmarks)
[![Errors](https://img.shields.io/badge/Errors-0-2ea44f?style=flat-square)](#benchmarks)
[![Max connections](https://img.shields.io/badge/Max%20connections-262144-0366d6?style=flat-square)](docs/server.md)
[![Peak memory](https://img.shields.io/badge/Peak%20memory-0%20alloc%20hello-2ea44f?style=flat-square)](#benchmarks)
[![Deps](https://img.shields.io/badge/Dependencies-0-lightgrey?style=flat-square)](go.mod)

</div>

---

**Status: v0.2.4 (GA for application embedding).** Suitable for production application servers (e.g. ZATRANO V3). Not positioned as a reverse proxy. Read [SECURITY.md](SECURITY.md) before public exposure.

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
go get github.com/zatrano/rawhttp@v0.2.4
```

Tests live in a separate module (`test/`); run with: `cd test && go test ./...`

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

The tables below are the host-specific snapshot already recorded in [docs/performance.md](docs/performance.md) (TCP multibench 2026-09-30, ServeConn 2026-10-02, Go 1.25.13, Windows/amd64, GOMAXPROCS=8, i5-1135G7 @ 2.40GHz). v0.2.4 does not change the serve path and was not re-measured. Absolute ns/RPS are **host-specific**. CI ServeConn floors are the regression contract.

Numbers below mix a **2026-09-30 TCP multibench snapshot** (RPS tables) with **2026-10-02 ServeConn gates + microbench** on this host. CI ServeConn floors are the regression contract (see [docs/performance.md](docs/performance.md)).

### TCP multi-rival (`scripts/multibench`)

```bash
cd scripts/multibench && go run . -c 64 -d 3s -rounds 3
```

Conditions: keep-alive HTTP/1.1; **same** `fasthttp.HostClient` for every server; c=64; 1s warmup + 3s timed; **3 rounds median** per server with **rotated start order**; scenarios `plaintext`, `json`, `headers`, `chunked`. Optional `-strict` is local-only (see [performance](docs/performance.md)); CI uses ServeConn gates.

Notes: Hertz on Windows used `network library=standard`. Multibench uses a shared `fasthttp.HostClient` for all servers.

#### plaintext — median RPS (snapshot)

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **RawHTTP** | **149 613** |
| 2 | fasthttp | 126 166 |
| 3 | gnet | 115 031 |
| 4 | Hertz | 108 968 |
| 5 | net/http | 76 284 |

#### json — median RPS (snapshot)

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **RawHTTP** | **141 835** |
| 2 | fasthttp | 132 769 |
| 3 | gnet | 131 427 |
| 4 | Hertz | 112 718 |
| 5 | net/http | 69 777 |

#### headers — median RPS (snapshot)

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **RawHTTP** | **133 871** |
| 2 | fasthttp | 119 144 |
| 3 | gnet | 104 071 |
| 4 | Hertz | 95 492 |
| 5 | net/http | 89 328 |

#### chunked (POST body echo) — median RPS (snapshot)

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **RawHTTP** | **139 057** |
| 2 | fasthttp | 130 533 |
| 3 | gnet | 129 751 |
| 4 | Hertz | 126 293 |
| 5 | net/http | 76 300 |

### ServeConn microbench (`test/`)

```bash
cd test && go test -run=^$ -bench='Benchmark(RawHTTP|FastHTTP|NetHTTP)_Plaintext$' -benchmem -benchtime=2s -count=3
cd test && go test -run=^$ -bench='Benchmark(RawHTTP|FastHTTP)_JSONPost$' -benchmem -benchtime=2s -count=3
```

Absolute ns/op below are **host-specific** (median of 5 runs, `-count=5`; re-measure on your machine).

#### Plaintext hello

| Server | ns/op | B/op | allocs/op | vs RawHTTP |
|--------|------:|-----:|----------:|-----------:|
| **RawHTTP** | **257** | 0 | **0** | — |
| fasthttp | 735 | 0 | 0 | 2.86× |
| net/http | 7562 | 1347 | 13 | 29.4× |

#### JSON POST

| Server | ns/op | B/op | allocs/op | vs RawHTTP |
|--------|------:|-----:|----------:|-----------:|
| **RawHTTP** | **458** | 0 | **0** | — |
| fasthttp | 863 | 0 | 0 | 1.88× |

#### ServeConn gate trimmed medians (this host, 2026-10-02)

| Scenario | vs | Ratio | Floor |
|----------|-----|------:|------:|
| plaintext | fasthttp | **2.84×** | 2.35× |
| JSON POST | fasthttp | **2.08×** | 1.65× |
| headers | fasthttp | **3.59×** | 1.50× |
| chunked | fasthttp | **2.09×** | 1.50× |
| plaintext | net/http | **24.9×** | 8.0× |
| JSON POST | net/http | **17.3×** | 4.0× |

### CI performance contract (v0.2.4)

ServeConn gates enforce “never slower” on trimmed rounds (≥1.00×) plus scenario floors (see [docs/performance.md](docs/performance.md) for soft 0.85× / trim / soft-retry). Floors today:

| Gate | Floor |
|------|-------|
| ServeConn vs fasthttp plaintext / JSON / headers / chunked | ≥2.35× / ≥1.65× / ≥1.5× / ≥1.5× |
| ServeConn vs net/http plaintext / JSON | ≥8.0× / ≥4.0× |
| HostClient vs fasthttp | measured in tests; not a CI floor |
| plaintext hello | **0 allocs/op** |

```bash
cd test && go test -run 'Gate|Allocs' -v
```

## vs net/http / fasthttp / Hertz / gnet

| | RawHTTP | net/http | fasthttp | Hertz | gnet |
|--|---------|----------|----------|-------|------|
| Role | HTTP/1.1 engine | stdlib HTTP | HTTP engine | CloudWeGo HTTP | Event-loop net framework |
| Deps | none | stdlib | compress libs | larger tree | event-loop |
| Router | bring your own | `ServeMux` | bring your own | built-in | N/A (raw) |
| Ctx model | `*Ctx` | `ResponseWriter`+`Request` | `RequestCtx` | `RequestContext` | custom |
| Typical use | engine under apps | general Go | Fiber / custom | microservices | custom protocols |
| This-host plaintext TCP (median snapshot) | **149.6k** | 76.3k | 126.2k | 109.0k | 115.0k |
| This-host ServeConn plaintext | **257 ns**, 0 alloc | 7562 ns, 13 alloc | 735 ns, 0 alloc | — | — |

Snapshot ranking is host-specific. Methodology and gate floors: [docs/performance.md](docs/performance.md).
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
| unit + security | `test/` + security corpus |
| race | race detector; also `-tags rawhttp_poison` |
| coverage / style / vuln / build | standard |
| gate | ServeConn rival floors + allocs (`TestGate_*` / `TestAllocs_*`) |
| fuzz | ServeConn, request-line, headers, chunked, differential ReadRequest |
| nightly fuzz | longer fuzz (schedule + `workflow_dispatch`) |

## Used by

[ZATRANO V3](https://github.com/zatrano) uses RawHTTP as its HTTP foundation.

## License

See [LICENSE](LICENSE).
