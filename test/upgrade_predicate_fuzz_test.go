package test_test

import (
	"encoding/base64"
	"strings"
	"testing"
)

// independentUpgradeOK is a stand-alone predicate (does not call rawhttp parser
// helpers). It mirrors AllowUpgrade admission rules for property fuzzing.
func independentUpgradeOK(raw string) bool {
	// Split request line + headers (stop at blank line).
	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	if head == raw {
		head, _, _ = strings.Cut(raw, "\n\n")
	}
	lines := strings.Split(head, "\r\n")
	if len(lines) == 1 {
		lines = strings.Split(head, "\n")
	}
	if len(lines) == 0 {
		return false
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 3 {
		return false
	}
	if parts[0] != "GET" {
		return false
	}
	if parts[2] != "HTTP/1.1" {
		return false
	}

	var (
		upgradeVals []string
		connVals    []string
		keys        []string
		versions    []string
		hasCL       bool
		hasTE       bool
	)
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		switch strings.ToLower(name) {
		case "upgrade":
			upgradeVals = append(upgradeVals, val)
		case "connection":
			connVals = append(connVals, val)
		case "sec-websocket-key":
			keys = append(keys, val)
		case "sec-websocket-version":
			versions = append(versions, val)
		case "content-length":
			hasCL = true
		case "transfer-encoding":
			hasTE = true
		}
	}
	if hasCL || hasTE {
		return false
	}
	if len(upgradeVals) != 1 {
		return false
	}
	if strings.Contains(upgradeVals[0], ",") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(upgradeVals[0]), "websocket") {
		return false
	}
	connUpgrade := false
	for _, cv := range connVals {
		// Tokenize like rawhttp parseConnectionDirectives: split on comma and OWS.
		rest := cv
		for len(rest) > 0 {
			rest = strings.TrimLeft(rest, " \t,")
			if len(rest) == 0 {
				break
			}
			end := 0
			for end < len(rest) && rest[end] != ',' && rest[end] != ' ' && rest[end] != '\t' {
				end++
			}
			tok := rest[:end]
			rest = rest[end:]
			if strings.EqualFold(tok, "upgrade") {
				connUpgrade = true
			}
		}
	}
	if !connUpgrade {
		return false
	}
	if len(versions) != 1 || strings.TrimSpace(versions[0]) != "13" {
		return false
	}
	if len(keys) != 1 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(keys[0])
	if err != nil || len(decoded) != 16 {
		return false
	}
	return true
}

// FuzzUpgradePredicate: with AllowUpgrade=true, if the handler runs and the
// raw request carries an Upgrade/Connection-upgrade signal in the header block,
// the independent predicate must hold for the same bytes.
func FuzzUpgradePredicate(f *testing.F) {
	for _, tc := range differentialCorpus() {
		f.Add([]byte(tc.raw))
	}
	key := "dGhlIHNhbXBsZSBub25jZQ==" // 16 bytes
	f.Add([]byte("GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 || len(data) > 8<<10 {
			t.Skip()
		}
		raw := string(data)
		called, status := rawhttpOutcomeAllow(raw, true)
		if !called {
			return
		}
		// Header-only signal (body may contain the word "upgrade").
		if !requestLooksLikeUpgrade(data) {
			return
		}
		if !independentUpgradeOK(raw) {
			t.Fatalf("handler called for upgrade-shaped request that fails independent predicate; status=%s\n---\n%s", status, raw)
		}
	})
}
