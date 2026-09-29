# rawhttp

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
[![Version](https://img.shields.io/github/v/tag/zatrano/rawhttp?filter=v*&sort=semver&label=version&color=blue)](https://github.com/zatrano/rawhttp/releases/tag/v0.1.0)
[![Latest Release](https://img.shields.io/github/v/release/zatrano/rawhttp?display_name=tag&label=latest&color=brightgreen)](https://github.com/zatrano/rawhttp/releases/latest)
[![Security Policy](https://img.shields.io/badge/Security-Policy-red?logo=github)](SECURITY.md)

[![Peak RPS](https://img.shields.io/badge/Peak%20RPS-159k-2ea44f?style=flat-square)](#benchmarks)
[![Errors](https://img.shields.io/badge/Errors-0-2ea44f?style=flat-square)](#benchmarks)
[![Max connections](https://img.shields.io/badge/Max%20connections-262144-0366d6?style=flat-square)](docs/server.md)
[![Peak memory](https://img.shields.io/badge/Peak%20memory-0%20alloc%20hello-2ea44f?style=flat-square)](#benchmarks)
[![Deps](https://img.shields.io/badge/Dependencies-0-lightgrey?style=flat-square)](go.mod)

**Status: v0.1.0 (experimental).** Suitable for controlled deployments and benchmarking. Read [SECURITY.md](SECURITY.md) before public exposure.

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
go get github.com/zatrano/rawhttp@v0.1.0
```

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
- Streaming (request/response), connection `Hijack`, timeouts
- Middleware helpers (CORS, compress, rate limit, auth, …) — compose by wrapping `Handler`
- Proxy dialers (HTTP CONNECT / SOCKS5), `TCPDialer` + DNS cache

**Not included in v0.1.0:** path-parameter router (`:id` / `{id}`), header-name normalization disable API, WebSocket helper (use `Hijack` + an external library).

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

Measured on **2026-09-29**, **v0.1.0**, Go **1.25.13**, Windows/amd64, GOMAXPROCS=8, CPU **i5-1135G7 @ 2.40GHz**. Absolute RPS varies by host; **rawhttp must be #1** on every scenario (CI `-strict`).

### TCP multi-rival (`scripts/multibench`)

```bash
cd scripts/multibench && go run . -c 64 -d 3s -rounds 3 -strict
```

Conditions: keep-alive HTTP/1.1; **same** `fasthttp.HostClient` for every server; c=64; 1s warmup + 3s timed; **3 rounds median** per server with **rotated start order**; scenarios `plaintext`, `json`, `headers`, `chunked`.

Gate: rawhttp **#1** on median snapshot RPS **and** pairwise median ≥ 1.00× vs every rival.

Notes: **gnet** = minimal keep-alive framer (waits for Content-Length body; not a full HTTP stack). **Hertz** on Windows used `network library=standard`.

#### plaintext — median RPS

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **rawhttp** | **159 183** |
| 2 | fasthttp | 153 225 |
| 3 | gnet | 146 162 |
| 4 | Hertz | 144 761 |
| 5 | net/http | 105 397 |

#### json — median RPS

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **rawhttp** | **155 373** |
| 2 | fasthttp | 147 584 |
| 3 | gnet | 144 903 |
| 4 | Hertz | 141 248 |
| 5 | net/http | 87 563 |

#### headers — median RPS

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **rawhttp** | **154 753** |
| 2 | fasthttp | 147 980 |
| 3 | Hertz | 143 457 |
| 4 | gnet | 142 904 |
| 5 | net/http | 101 655 |

#### chunked (POST body echo) — median RPS

| Rank | Server | req/s |
|-----:|--------|------:|
| 1 | **rawhttp** | **155 058** |
| 2 | fasthttp | 129 449 |
| 3 | Hertz | 124 150 |
| 4 | gnet | 124 015 |
| 5 | net/http | 66 454 |

Result: **OK** — rawhttp #1 on all four scenarios.

### ServeConn microbench (`test/`)

```bash
cd test && go test -run=^$ -bench='Benchmark(RawHTTP|FastHTTP|NetHTTP)_Plaintext$' -benchmem -benchtime=2s -count=1
cd test && go test -run=^$ -bench='Benchmark(RawHTTP|FastHTTP)_JSONPost$' -benchmem -benchtime=2s -count=1
```

#### Plaintext hello

| Server | ns/op | B/op | allocs/op | vs rawhttp |
|--------|------:|-----:|----------:|-----------:|
| **rawhttp** | **203.3** | 0 | **0** | — |
| fasthttp | 548.2 | 0 | 0 | 2.70× |
| net/http | 2591 | 1333 | 13 | 12.7× |

#### JSON POST

| Server | ns/op | B/op | allocs/op | vs rawhttp |
|--------|------:|-----:|----------:|-----------:|
| **rawhttp** | **453.8** | 0 | **0** | — |
| fasthttp | 936.2 | 0 | 0 | 2.06× |

### CI performance contract (v0.1.0)

| Gate | Floor |
|------|-------|
| ServeConn vs fasthttp plaintext / JSON / headers / chunked | ≥3.0× / ≥1.65× / ≥1.5× / ≥1.5× |
| ServeConn vs net/http plaintext / JSON | ≥8.0× / ≥4.0× |
| HostClient vs fasthttp | ≥1.15× |
| plaintext hello | **0 allocs/op** |
| multibench `-strict` | **#1 median snapshot** + pairwise ≥ 1.00× vs all rivals |

```bash
cd test && go test -run 'Gate|Allocs' -v
cd scripts/multibench && go run . -c 64 -d 3s -rounds 5 -strict
```

## vs net/http / fasthttp / Hertz / gnet

| | rawhttp | net/http | fasthttp | Hertz | gnet |
|--|---------|----------|----------|-------|------|
| Role | HTTP/1.1 engine | stdlib HTTP | HTTP engine | CloudWeGo HTTP | Event-loop net framework |
| Deps | none | stdlib | compress libs | larger tree | event-loop |
| Router | bring your own | `ServeMux` | bring your own | built-in | N/A (raw) |
| Ctx model | `*Ctx` | `ResponseWriter`+`Request` | `RequestCtx` | `RequestContext` | custom |
| Typical use | engine under apps | general Go | Fiber / custom | microservices | custom protocols |
| This-host plaintext TCP (median) | **159.2k #1** | 105.4k | 153.2k | 144.8k | 146.2k† |
| This-host ServeConn plaintext | **203 ns**, 0 alloc | 2591 ns, 13 alloc | 548 ns, 0 alloc | — | — |

† gnet multibench is a minimal framer, not full HTTP parity.

RawHTTP is **not** a fasthttp fork or wrapper. Full tables: [docs/performance.md](docs/performance.md).

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
| race / coverage / style / vuln / build | standard |
| gate | ServeConn rivals + allocs |
| fuzz | ServeConn, request-line, headers, chunked |

## Used by

[ZATRANO V3](https://github.com/zatrano) uses RawHTTP as its HTTP foundation.

## License

See [LICENSE](LICENSE).
