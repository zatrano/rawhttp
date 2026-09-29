package rawhttp

// HealthCheck is a tiny liveness handler (200 + "ok").
func HealthCheck(ctx *Ctx) {
	ctx.SetContentType("text/plain")
	ctx.SetBodyString("ok")
}

// ReadyCheck reports readiness. When ready is nil or returns true → 200 "ready";
// otherwise 503 "not ready".
func ReadyCheck(ready func() bool) Handler {
	return func(ctx *Ctx) {
		if ready != nil && !ready() {
			ctx.Error("not ready", 503)
			return
		}
		ctx.SetContentType("text/plain")
		ctx.SetBodyString("ready")
	}
}

// RecoverMiddleware recovers panics from h, logs via Server is not available here
// so it writes 500 and optionally calls onPanic.
func RecoverMiddleware(h Handler, onPanic func(any)) Handler {
	return func(ctx *Ctx) {
		defer func() {
			if v := recover(); v != nil {
				if onPanic != nil {
					onPanic(v)
				}
				ctx.Error("Internal Server Error", 500)
			}
		}()
		h(ctx)
	}
}
