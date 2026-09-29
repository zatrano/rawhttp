package rawhttp

import (
	"encoding/base64"
)

// BasicAuth parses Authorization: Basic credentials.
// ok is false when the header is missing or not valid Basic auth.
func (c *Ctx) BasicAuth() (user, pass string, ok bool) {
	h := c.authorization
	if len(h) == 0 {
		h = c.Header("Authorization")
	}
	const prefix = "Basic "
	if len(h) < len(prefix) || !equalFoldStr(h[:len(prefix)], prefix) {
		return "", "", false
	}
	dec, err := base64.StdEncoding.DecodeString(string(h[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	for i, b := range dec {
		if b == ':' {
			return string(dec[:i]), string(dec[i+1:]), true
		}
	}
	return "", "", false
}

// SetBasicAuthChallenge sets 401 Unauthorized with a WWW-Authenticate Basic challenge.
func (c *Ctx) SetBasicAuthChallenge(realm string) {
	if realm == "" {
		realm = "Restricted"
	}
	if containsCTLOrCRLF(realm) {
		realm = "Restricted"
	}
	c.SetStatusCode(401)
	_ = c.SetHeader("WWW-Authenticate", `Basic realm="`+realm+`"`)
	c.SetBodyString("Unauthorized")
}
