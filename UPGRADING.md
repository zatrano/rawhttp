# Upgrading

## v1.0.0

No source changes are needed from v0.2.10: `go get github.com/zatrano/rawhttp@v1.0.0`. The import path stays `github.com/zatrano/rawhttp`.

## v0.2.9

A still-valid read or write deadline is not refreshed. The effective timeout is `[d − granule, d]`, granule = min(d/8, 1s). A kept deadline is earlier than `now+d` by at most one granule. It is never later than `d`.

The wait between keep-alive requests is `IdleTimeout` (90s when unset), not `ReadTimeout`. `ReadTimeout` covers a request body and the read once a request has started. An idle connection closes on `IdleTimeout`. A reader that does not drain the body is cut by `WriteTimeout`.
