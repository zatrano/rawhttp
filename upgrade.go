package rawhttp

import (
	"encoding/base64"
)

var (
	vWebsocket = []byte("websocket")
	v13        = []byte("13")
)

// validateWebSocketUpgrade enforces the AllowUpgrade admission rules after
// headers are parsed. Connection is already tokenized (so
// "keep-alive, Upgrade" from Firefox is fine). Upgrade must be exactly the
// single token "websocket". Sec-WebSocket-Key must base64-decode to 16 bytes.
func validateWebSocketUpgrade(ctx *Ctx) error {
	if !ctx.upgradeWanted || len(ctx.upgradeProto) == 0 {
		return ErrBadRequest
	}
	if !isGetMethod(ctx.Method) || ctx.httpMinor != 1 {
		return ErrBadRequest
	}
	proto := trimOWS(ctx.upgradeProto)
	if len(proto) == 0 || !equalFoldBytes(proto, vWebsocket) {
		return ErrBadRequest
	}
	// Reject multi-value Upgrade (comma) — must be exactly websocket.
	for _, c := range proto {
		if c == ',' {
			return ErrBadRequest
		}
	}
	if ctx.clSet || ctx.chunked {
		return ErrBadRequest
	}
	key := ctx.Header("Sec-WebSocket-Key")
	ver := trimOWS(ctx.Header("Sec-WebSocket-Version"))
	if len(key) == 0 || !equalFoldBytes(ver, v13) {
		return ErrBadRequest
	}
	if headerNameCount(ctx, "Sec-WebSocket-Key") != 1 {
		return ErrBadRequest
	}
	if headerNameCount(ctx, "Sec-WebSocket-Version") != 1 {
		return ErrBadRequest
	}
	if headerNameCount(ctx, "Upgrade") != 1 {
		return ErrBadRequest
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(key)))
	n, err := base64.StdEncoding.Decode(decoded, key)
	if err != nil || n != 16 {
		return ErrBadRequest
	}
	return nil
}

func headerNameCount(ctx *Ctx, name string) int {
	n := 0
	ctx.VisitHeader(func(k, v []byte) {
		if equalFoldStr(k, name) {
			n++
		}
	})
	return n
}

func trimOWS(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}
