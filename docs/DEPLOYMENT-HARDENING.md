# Deployment hardening

rawhttp is meant to face clients directly. Every check in
[SECURITY-GUARANTEES.md](SECURITY-GUARANTEES.md) runs in the process, so the
guarantees do not depend on anything in front of it. This page covers the host
around the process: a Linux server managed with apt and systemd.

## Build

- Build with a supported Go patch release (`go version` on the build host). A
  distribution `golang-go` package is often behind; use the official toolchain
  from go.dev/dl or `GOTOOLCHAIN=go1.26.9` (or newer).
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usr/local/bin/app ./cmd/app`
- Run `govulncheck ./...` before each release.

## Service account and systemd unit

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin app
sudo install -d -o app -g app -m 0750 /var/lib/app
```

`/etc/systemd/system/app.service`:

```ini
[Unit]
Description=app
After=network-online.target
Wants=network-online.target

[Service]
User=app
Group=app
ExecStart=/usr/local/bin/app
WorkingDirectory=/var/lib/app
EnvironmentFile=/etc/app/env
Restart=on-failure
RestartSec=2s

# Bind 443/80 without root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true

ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/app
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallArchitectures=native

# Per-connection limits come from rawhttp; these cap the process.
LimitNOFILE=262144
TasksMax=4096
MemoryMax=2G

[Install]
WantedBy=multi-user.target
```

```bash
sudo install -d -m 0750 -o root -g app /etc/app
sudo install -m 0640 -o root -g app env /etc/app/env
sudo systemctl daemon-reload
sudo systemctl enable --now app
systemd-analyze security app
```

Keep secrets (`APP_KEY`, database passwords) in `/etc/app/env` with mode 0640,
not in the unit file.

`LimitNOFILE` must be at least the `Concurrency` you allow plus the files the
application opens. `MemoryMax` should be above the worst case of
`Concurrency × (MaxHeaderBytes + MaxRequestBodySize)` you expect to admit, or
lower `Concurrency` until it is.

## Kernel and network

`/etc/sysctl.d/90-app.conf`:

```ini
net.core.somaxconn = 4096
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_syncookies = 1
net.ipv4.ip_local_port_range = 10240 65535
```

Allow only the ports the service listens on:

```bash
sudo apt install nftables   # or ufw
sudo ufw default deny incoming
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

## TLS

- Certificates: `sudo apt install certbot`, then
  `certbot certonly --standalone -d example.com` during a maintenance window or
  `--webroot` with a static challenge route. Reload the service from a
  `--deploy-hook`.
- Serve with `ListenAndServeTLS`, or build the config with
  `rawhttp.ServerTLSConfig(cert)`; both set TLS 1.2 minimum and AEAD-only TLS 1.2
  suites.
- Redirect port 80 to 443 in the application, and send HSTS once every host
  name serves TLS.

## Server settings

Start from the "Recommended production values" in
[SECURITY-BASELINE.md](SECURITY-BASELINE.md): a short `ReadHeaderTimeout`, a
body cap sized for the largest real upload, `MaxURILength`, and
`MaxConnsPerIP`. Leave `TrustedProxies` empty unless a proxy you run sits in
front of the process.

## A proxy or CDN in front (optional extra layer)

A reverse proxy (Nginx) or a CDN can add caching, volumetric DDoS absorption
and geographic distribution. It is an extra layer: rawhttp still applies every
framing rule to what the proxy sends, and the guarantees above do not rely on
the proxy. When you add one:

- Put only the proxy addresses in `TrustedProxies`, never `0.0.0.0/0` or
  `::/0`. `ClientIP` then reads `X-Forwarded-For` from the right.
- Make the proxy overwrite, not append to, any forwarding header it does not
  control end to end, or make sure the proxy is the only writer.
- Firewall the origin so only the proxy can reach it; otherwise clients can
  bypass the proxy and its limits.
- Keep proxy limits at or below the rawhttp limits (body size, header size,
  timeouts) so the two layers fail the same request the same way.

Nginx example:

```nginx
upstream app { server 127.0.0.1:8080; keepalive 64; }

server {
    listen 443 ssl;
    server_name example.com;
    client_max_body_size 1m;
    large_client_header_buffers 4 8k;

    location / {
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass http://app;
    }
}
```

`proxy_set_header X-Forwarded-For $remote_addr` replaces whatever the client
sent, so the application sees one hop written by the proxy.

## Updates

- `sudo apt update && sudo apt upgrade` on a schedule, or
  `unattended-upgrades` for security updates.
- Rebuild and redeploy when Go publishes a security release; the binary carries
  its own standard library.
