# Changelog

## v0.2.0 - 2026-09-30

### Added

- `Server.AllowUpgrade` — opt-in admission of a standards-shaped WebSocket handshake to the handler for `Hijack` (default remains reject-with-400). Connection header is tokenized (Firefox `keep-alive, Upgrade`); `Sec-WebSocket-Key` must base64-decode to 16 bytes.
- `rawhttp_poison` build tag — after Handler, fills pinned request buffer / owned copies with `0xDE` and clears Ctx request slices so accidental retention fails in tests. Default builds use empty stubs (no cost).
- Differential / upgrade property fuzz (`FuzzDifferentialReadRequest`, `FuzzDifferentialNetHTTP`, `FuzzUpgradePredicate`).
- Nightly fuzz workflow (`schedule` + `workflow_dispatch`); CI race job also runs under `-tags rawhttp_poison`.

### Changed

- ServeConn gate floors recalibrated (plaintext ≥2.35×); HostClient timing is informational only.
- `scripts/multibench -strict` is local-only; CI ranking is informational. Blocking gates remain `TestGate_*` (ServeConn).
- `Hijack` clears connection deadlines so the caller owns timeouts.

### Documentation

- README / SECURITY / `docs/hijacking.md` / `docs/performance.md` / `docs/production.md` aligned with Upgrade rejection, AllowUpgrade, poison testing, per-connection memory, and hijacked-connection accounting.
