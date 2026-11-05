---
title: Behind a proxy or custom TLS
description: Run Bunker behind nginx, Caddy, Traefik or a load balancer, or give it your own certificate.
sidebar: { order: 6 }
---

Bunker speaks plain HTTP (and HTTP/2 without TLS, "h2c") by default. You can put TLS in front of it, let it get certificates itself, or give it a certificate file.

## How Bunker picks a TLS mode

`BUNKER_TLS` can be `auto`, `file` or `off`. When it isn't set:

| Situation | Mode |
|---|---|
| `BUNKER_TLS_CERT` is set | `file` |
| A platform `PORT` is set, dev mode, `BUNKER_LISTEN` is set, the URL isn't `https://`, the URL is localhost, or Bunker runs in a container | `off` (plain HTTP on `:8080`) |
| Otherwise: a bare machine with an `https://` `BUNKER_URL` | `auto` (Let's Encrypt on `:443`, challenges and redirect on `:80`) |

## Behind a reverse proxy

Set the public URL and listen on localhost:

```sh
BUNKER_URL=https://bunker.example.com
BUNKER_LISTEN=127.0.0.1:8080
BUNKER_TRUST_PROXY=true
```

`BUNKER_TRUST_PROXY` makes Bunker take the client IP from `X-Forwarded-For` (used for rate limits and the audit log). It's on by default whenever TLS is `off` or a platform `PORT` is set. Turn it off if clients can reach Bunker directly.

**Don't buffer server-sent events.** Live updates use long-lived responses.

Caddy:

```txt
bunker.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

nginx:

```nginx
location / {
  proxy_pass http://127.0.0.1:8080;
  proxy_http_version 1.1;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;
  proxy_buffering off;
  proxy_read_timeout 1h;
  client_max_body_size 100m;   # match BUNKER_UPLOAD_MAX_MB
}
```

Bunker uses one hostname for everything: no wildcard DNS or certificate is needed. Pass the `Host` header through unchanged.

## Your own certificate

```sh
BUNKER_URL=https://bunker.example.com
BUNKER_TLS_CERT=/etc/bunker/tls/fullchain.pem
BUNKER_TLS_KEY=/etc/bunker/tls/privkey.pem
BUNKER_LISTEN=:443
```

## Automatic certificates (built in)

```sh
BUNKER_URL=https://bunker.example.com
BUNKER_TLS=auto
BUNKER_ACME_EMAIL=it@example.com          # optional
# BUNKER_ACME_DIRECTORY=https://...       # another ACME CA, e.g. your internal one
# BUNKER_HTTP_LISTEN=:80                  # where challenges + the HTTPS redirect listen
```

Certificates are stored in `<data dir>/certs`. TLS 1.2 is the minimum. HSTS (`max-age=31536000`) is sent whenever the URL is `https://`.
