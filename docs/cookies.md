# Cookies

## Read

```go
v := ctx.Cookie("session")
ctx.VisitCookie(func(name, val []byte) {})
```

## Write

```go
_ = ctx.SetCookie(&rawhttp.Cookie{
	Name:     "session",
	Value:    "abc",
	Path:     "/",
	HTTPOnly: true,
	Secure:   true,
	SameSite: rawhttp.SameSiteLaxMode,
	MaxAge:   3600,
})
```

`SameSite` constants: `SameSiteDefaultMode`, `SameSiteLaxMode`, `SameSiteStrictMode`, `SameSiteNoneMode`.

`SameSite=None` forces `Secure` in this implementation.

## Helpers

```go
_ = ctx.DeleteCookie("session")
_ = ctx.SecureCookie("session", "abc") // (name, value); Secure+HttpOnly defaults
```
