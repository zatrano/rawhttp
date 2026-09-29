package rawhttp

import (
	"sync"
	"time"
)

// RateLimitOptions configures RateLimitMiddleware.
type RateLimitOptions struct {
	// Rate is allowed requests per second (token refill). Required > 0.
	Rate float64
	// Burst is the max tokens (bucket size). Zero → max(1, ceil(Rate)).
	Burst int
	// Key extracts the rate-limit key. Nil → ClientIP (falls back to RemoteIP).
	Key func(ctx *Ctx) string
	// StatusCode on reject. Zero → 429.
	StatusCode int
	// Message body on reject. Empty → "Too Many Requests".
	Message string
}

type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	rate   float64
	burst  float64
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if !b.last.IsZero() {
		elapsed := now.Sub(b.last).Seconds()
		b.tokens += elapsed * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	} else {
		b.tokens = b.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimitMiddleware rejects excess requests with 429 using a per-key token bucket.
func RateLimitMiddleware(h Handler, opt RateLimitOptions) Handler {
	if opt.Rate <= 0 {
		return h
	}
	burst := opt.Burst
	if burst <= 0 {
		burst = int(opt.Rate)
		if burst < 1 {
			burst = 1
		}
	}
	code := opt.StatusCode
	if code == 0 {
		code = 429
	}
	msg := opt.Message
	if msg == "" {
		msg = "Too Many Requests"
	}
	keyFn := opt.Key
	if keyFn == nil {
		keyFn = func(ctx *Ctx) string {
			if ip := ctx.ClientIP(); ip != "" {
				return ip
			}
			return ctx.RemoteIP()
		}
	}
	var mu sync.Mutex
	buckets := make(map[string]*tokenBucket)
	return func(ctx *Ctx) {
		key := keyFn(ctx)
		mu.Lock()
		b := buckets[key]
		if b == nil {
			b = &tokenBucket{rate: opt.Rate, burst: float64(burst), tokens: float64(burst), last: time.Now()}
			buckets[key] = b
		}
		mu.Unlock()
		if !b.allow() {
			ctx.Error(msg, code)
			return
		}
		h(ctx)
	}
}
