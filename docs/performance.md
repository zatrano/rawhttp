# Performance

## Design (code-backed)

RawHTTP optimizes the HTTP/1.1 hot path by doing less work per request:

1. **Buffer-slice parsing** — on the common path, `Method` / `Path` / headers are slices into the read buffer.
2. **Pinned small-body path** — Content-Length bodies that fit beside headers skip ownership copies of Method/Path/headers.
3. **No `bufio` on the server hot path** — custom `connReader` with pin/off separation.
4. **Keep-alive response cache** — `SetBody` replies reuse an encoded wire buffer (including Content-Type when set).
5. **Extra-header index** — non-indexed headers recorded at parse time for cheap `Header()` peeks.
6. **Optional features stay cold** — e.g. `StreamRequestBody` is opt-in.

## Guidance (honest)

- **CI ServeConn floors are authoritative** for regressions (`test/gate_test.go`).
- README / tables below are **host-specific snapshots**; absolute ns and TCP RPS move with CPU load and GOMAXPROCS.
- On same-host TCP multi-rival runs, rawhttp / fasthttp / gnet often share a band; run-to-run ranking can flip. Do not treat “always #1 on every TCP scenario” as a product guarantee.

## Measured results (snapshot)

| Field | Value |
|-------|--------|
| Date | 2026-09-29 |
| Go | 1.25.13 windows/amd64 |
| GOMAXPROCS | 8 |
| CPU | 11th Gen Intel Core i5-1135G7 @ 2.40GHz |
| Command | `go run . -c 64 -d 3s -rounds 3` in `scripts/multibench` |

### Median snapshot RPS (one host)

| Scenario | rawhttp | fasthttp | gnet | Hertz | net/http |
|----------|--------:|---------:|-----:|------:|---------:|
| plaintext | **159 183** | 153 225 | 146 162 | 144 761 | 105 397 |
| json | **155 373** | 147 584 | 144 903 | 141 248 | 87 563 |
| headers | **154 753** | 147 980 | 142 904 | 143 457 | 101 655 |
| chunked | **155 058** | 129 449 | 124 015 | 124 150 | 66 454 |

gnet = minimal keep-alive framer (waits for body bytes; not full HTTP). Hertz on Windows used `standard` network.

### ServeConn microbench

| Bench | rawhttp | fasthttp | net/http |
|-------|--------:|---------:|---------:|
| Plaintext ns/op (allocs) | **203.3 (0)** | 548.2 (0) | 2591 (13) |
| JSON POST ns/op (allocs) | **453.8 (0)** | 936.2 (0) | — |

Typical plaintext vs fasthttp band on ServeConn: about **2.5–3×** with **0 allocs** (exact ratio is host-specific; CI floor is separate).

## CI ServeConn gate mechanics

Implemented in `test/gate_test.go` (`assertFaster` / `assertFasterOnce`):

1. Warmup, then **11** timed rounds (order of rawhttp vs rival alternates).
2. Each round ratio = `rival_ns / rawhttp_ns`. Soft per-round floor: ratio ≥ **0.85×** (a single noisy round may be &lt; 1.00×).
3. Sort ratios; **trim** the 2 lowest and 2 highest.
4. Every **trimmed** ratio must be ≥ **1.00×** (“never slower” after trim).
5. **Trimmed median** must be ≥ the scenario floor (plaintext / JSON / headers / chunked / net/http / client).
6. On failure, **one soft-retry** after `runtime.GC()` + 100ms sleep (floors unchanged).

| Test | Floor (trimmed median) |
|------|------------------------|
| ServeConn vs fasthttp | plaintext ≥3.0×, JSON ≥1.65×, headers/chunked ≥1.5× |
| ServeConn vs net/http | plaintext ≥8.0×, JSON ≥4.0× |
| HostClient vs fasthttp | ≥1.15× |
| Allocs plaintext hello | 0 |

Optional `scripts/multibench -strict` is a **separate**, noisier TCP ranking helper (same-process load client); treat CI ServeConn gates as the regression contract.

```bash
cd test && go test -run 'Gate|Allocs' -count=1 -v
cd scripts/multibench && go run . -c 64 -d 3s -rounds 5
```
