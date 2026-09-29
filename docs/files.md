# Files

## Single file

```go
ctx.SendFile("/path/to/file") // no return value
_ = rawhttp.ServeFile(ctx, "/path/to/file")
```

Supports conditional / range behavior as implemented in `sendfile.go` / FS stack.

## Static file server

```go
fs := rawhttp.NewFS(rawhttp.FS{
	Root:           "/var/www",
	IndexNames:     []string{"index.html"},
	Compress:       true, // sibling .br / .gz
	RejectSymlinks: true,
	HideDotFiles:   true,
})
s := &rawhttp.Server{Handler: fs.Handler()}
```

`FS` also supports `io/fs` / `embed.FS` via the `FS` field (see `fs.go` comments and fields).

`(*FS).Serve(ctx, path)` serves one path under the FS root.
