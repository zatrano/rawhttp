# Concepts

## Layers

```text
Your app / framework     ← routing, sessions, DB, templates, …
        ↓
     RawHTTP             ← HTTP/1.1 parse, connections, Ctx, client
        ↓
     Network
```

| | RawHTTP | Your app / framework |
|--|---------|----------------------|
| Role | HTTP engine | Application logic |
| Routing | Manual / optional | Usually owns routes |
| Business logic | No | Yes |

RawHTTP is not a fasthttp fork or wrapper. Frameworks may sit on top of it the same way Fiber sits on fasthttp — that is a structural analogy only.


## Core objects

```text
Server
  ├── Listener (net.Listener)
  ├── Connection (per accept)
  │     ├── read request → Ctx
  │     ├── Handler(ctx)
  │     └── write response
  └── Handler
```

### Server

`rawhttp.Server` holds configuration and serves connections via `Serve`, `ServeConn`, or `ListenAndServe`.

### Handler

```go
type Handler func(ctx *Ctx)
```

One function per request. Compose behavior by wrapping handlers (middleware).

### Ctx

`*Ctx` is the request/response cycle object: method, path, headers, body, response builders. Values that alias the read buffer must not be retained after the handler returns (unless copied).

### Connection lifecycle

1. Accept
2. Optional idle wait (keep-alive)
3. Parse request line + headers (+ body)
4. Call `Handler`
5. Write response (unless hijacked)
6. Close or keep-alive

`ConnState` callbacks: `StateNew`, `StateActive`, `StateIdle`, `StateClosed`.
