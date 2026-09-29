package rawhttp

// SecureHeadersOptions configures SecureHeadersMiddleware.
type SecureHeadersOptions struct {
	// FrameOptions sets X-Frame-Options. Empty → "DENY".
	FrameOptions string
	// ContentTypeOptions sets X-Content-Type-Options. Empty → "nosniff".
	ContentTypeOptions string
	// ReferrerPolicy sets Referrer-Policy. Empty → "no-referrer".
	ReferrerPolicy string
	// XSSProtection sets X-XSS-Protection. Empty omits (legacy).
	XSSProtection string
	// HSTS, when non-empty, sets Strict-Transport-Security (only when ctx.IsTLS()).
	HSTS string
	// ContentSecurityPolicy, when non-empty, sets Content-Security-Policy.
	ContentSecurityPolicy string
	// PermissionsPolicy, when non-empty, sets Permissions-Policy.
	PermissionsPolicy string
	// CrossOriginOpenerPolicy, when non-empty, sets Cross-Origin-Opener-Policy.
	CrossOriginOpenerPolicy string
}

// SecureHeadersMiddleware sets common hardening response headers after h runs.
func SecureHeadersMiddleware(h Handler, opt SecureHeadersOptions) Handler {
	frame := opt.FrameOptions
	if frame == "" {
		frame = "DENY"
	}
	cto := opt.ContentTypeOptions
	if cto == "" {
		cto = "nosniff"
	}
	ref := opt.ReferrerPolicy
	if ref == "" {
		ref = "no-referrer"
	}
	return func(ctx *Ctx) {
		h(ctx)
		_ = ctx.SetHeader("X-Content-Type-Options", cto)
		_ = ctx.SetHeader("X-Frame-Options", frame)
		_ = ctx.SetHeader("Referrer-Policy", ref)
		if opt.XSSProtection != "" {
			_ = ctx.SetHeader("X-XSS-Protection", opt.XSSProtection)
		}
		if opt.HSTS != "" && ctx.IsTLS() {
			_ = ctx.SetHeader("Strict-Transport-Security", opt.HSTS)
		}
		if opt.ContentSecurityPolicy != "" {
			_ = ctx.SetHeader("Content-Security-Policy", opt.ContentSecurityPolicy)
		}
		if opt.PermissionsPolicy != "" {
			_ = ctx.SetHeader("Permissions-Policy", opt.PermissionsPolicy)
		}
		if opt.CrossOriginOpenerPolicy != "" {
			_ = ctx.SetHeader("Cross-Origin-Opener-Policy", opt.CrossOriginOpenerPolicy)
		}
	}
}
