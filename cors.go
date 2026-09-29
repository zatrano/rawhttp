package rawhttp

// CORSOptions configures CORSMiddleware.
type CORSOptions struct {
	// AllowOrigin is used when AllowOrigins is empty. Empty → "*".
	AllowOrigin string
	// AllowOrigins, when non-empty, reflects a matching request Origin
	// (sets Access-Control-Allow-Origin to that Origin and Vary: Origin).
	AllowOrigins  []string
	AllowMethods  string // empty → "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	AllowHeaders  string // empty → "Content-Type, Authorization"
	ExposeHeaders string
	// AllowCredentials adds Access-Control-Allow-Credentials.
	// Ignored when the effective Allow-Origin would be "*".
	AllowCredentials bool
	MaxAge           int // seconds; 0 omits Access-Control-Max-Age
}

// CORSMiddleware adds CORS response headers and short-circuits OPTIONS preflight.
func CORSMiddleware(h Handler, opt CORSOptions) Handler {
	if opt.AllowOrigin == "" && len(opt.AllowOrigins) == 0 {
		opt.AllowOrigin = "*"
	}
	if opt.AllowMethods == "" {
		opt.AllowMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	}
	if opt.AllowHeaders == "" {
		opt.AllowHeaders = "Content-Type, Authorization"
	}
	return func(ctx *Ctx) {
		acao, varyOrigin := resolveCORSOrigin(opt, ctx.Header("Origin"))
		if acao != "" {
			_ = ctx.SetHeader("Access-Control-Allow-Origin", acao)
		}
		if varyOrigin {
			_ = ctx.SetHeader("Vary", "Origin")
		}
		if opt.AllowCredentials && acao != "" && acao != "*" {
			_ = ctx.SetHeader("Access-Control-Allow-Credentials", "true")
		}
		if len(opt.ExposeHeaders) > 0 {
			_ = ctx.SetHeader("Access-Control-Expose-Headers", opt.ExposeHeaders)
		}
		if equalFoldStr(ctx.Method, "OPTIONS") {
			_ = ctx.SetHeader("Access-Control-Allow-Methods", opt.AllowMethods)
			_ = ctx.SetHeader("Access-Control-Allow-Headers", opt.AllowHeaders)
			if opt.MaxAge > 0 {
				_ = ctx.SetHeader("Access-Control-Max-Age", itoa(opt.MaxAge))
			}
			ctx.SetStatusCode(204)
			return
		}
		h(ctx)
	}
}

func resolveCORSOrigin(opt CORSOptions, origin []byte) (acao string, vary bool) {
	if len(opt.AllowOrigins) > 0 {
		if len(origin) == 0 {
			return "", true
		}
		o := string(origin)
		for _, allowed := range opt.AllowOrigins {
			if allowed == "*" {
				// Wildcard in list: reflect request origin (never "*"+credentials).
				return o, true
			}
			if allowed == o {
				return o, true
			}
		}
		return "", true
	}
	return opt.AllowOrigin, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
