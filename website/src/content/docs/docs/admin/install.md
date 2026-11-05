---
title: Install
description: Run Bunker on a single VM with Docker or the standalone binary.
sidebar: { order: 1 }
---

:::caution[Planned]
Release binaries and images are not published yet. These steps show the intended setup.
:::

## Requirements
- A Linux VM. 2 vCPU and 4 GB RAM covers about 5,000 users.
- A DNS name, for example `bunker.corp.com`. No wildcard DNS is needed.
- A Microsoft Entra ID app registration. See [Microsoft Entra ID](/bunker/docs/admin/entra/).
- Ports 80 and 443 open, for automatic HTTPS. You can also run Bunker behind your own proxy.

## Docker
```sh
docker run -d --name bunker --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v bunker-data:/data \
  -e BUNKER_URL=https://bunker.corp.com \
  -e BUNKER_ENTRA_TENANT_ID=<tenant-id> \
  -e BUNKER_ENTRA_CLIENT_ID=<client-id> \
  -e BUNKER_ENTRA_CLIENT_SECRET=<secret> \
  -e BUNKER_ADMINS=it-admins@corp.com \
  ghcr.io/kucukkanat/bunker:latest
```

## Binary
```sh
curl -fsSL https://github.com/kucukkanat/bunker/releases/latest/download/bunker-linux-amd64 -o /usr/local/bin/bunker
chmod +x /usr/local/bin/bunker
bunker serve --config /etc/bunker/bunker.toml
```
A systemd unit ships with each release.

## Behind a reverse proxy
Set `BUNKER_TLS=off` and `BUNKER_LISTEN=127.0.0.1:8080`, then forward `X-Forwarded-Proto` and `X-Forwarded-For`. Bunker refuses to serve over plain HTTP unless the request came through a trusted proxy.

## First run
Bunker checks the configuration at startup and **refuses to start** if:
- the Entra app allows multi-tenant or personal Microsoft accounts,
- `BUNKER_URL` isn't HTTPS,
- no admin is configured.

Then open `BUNKER_URL`, sign in as an admin, and go to **Admin → AI** to [pick a model provider](/bunker/docs/admin/ai/).
