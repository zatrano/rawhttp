# Concurrency

## Server model

Each accepted connection is served on its own goroutine (`Serve` accept loop). Multiple requests may run on that connection sequentially (HTTP/1.1 keep-alive / optional pipelining).

`Concurrency` caps concurrent connections. Excess waits up to `ConcurrencyWaitTimeout` or is rejected.

`MaxConnsPerIP` limits per-peer concurrency (`ErrPerIPConnLimit`).

## Ctx / buffer safety

- `*Ctx` is **request-scoped**. Do not share across goroutines without external synchronization.
- `TimeoutHandler` enables an internal write mutex for late writes after timeout.
- Header/path/body slices alias connection buffers — invalid after handler return.
- Client `Request`/`Response` from the pool must be `Release*`d; do not use after release.

## Pools

- `ctxPool` reuses `Ctx` objects between requests.
- Client: `AcquireRequest` / `ReleaseRequest`, `AcquireResponse` / `ReleaseResponse`.
- Connection pools on `Client` / `HostClient` (`LIFO` default, optional `FIFO`).

## Prefork / reuseport

`Prefork` and `ListenReusePort` exist for multi-process / SO_REUSEPORT deployments (platform-dependent). Child detection: `PreforkIsChild()`.
