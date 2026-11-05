---
title: Docker and Compose
description: Run the Bunker container with a volume, with Docker Compose, or with Compose plus Caddy for automatic HTTPS.
sidebar: { order: 2 }
---

The image is `ghcr.io/kucukkanat/bunker` (multi-arch, distroless, runs as uid 65532). Everything Bunker stores lives in `/data` inside the container.

:::tip[Before the first release]
If the image isn't published yet, build it straight from GitHub: `docker build -t ghcr.io/kucukkanat/bunker https://github.com/kucukkanat/bunker.git`. The Compose files do this for you automatically when the pull fails.
:::

## docker run

```sh
docker volume create bunker-data
docker run -d --name bunker -p 8080:8080 \
  -v bunker-data:/data --restart unless-stopped \
  ghcr.io/kucukkanat/bunker
docker logs bunker   # setup URL and one-time code
```

Open `http://<host>:8080/setup`, enter the code and create your admin account.

Once Bunker has a real address, tell it: `-e BUNKER_URL=https://bunker.example.com`. Without it, Bunker learns its URL from the first request with a real hostname and saves it.

:::note
Inside a container Bunker never tries to grab ports 80 and 443 for automatic HTTPS. Put a proxy in front for TLS (see below), or set `BUNKER_TLS=auto` yourself and publish ports 80 and 443.
:::

## Docker Compose

`docker-compose.yml` in the repository:

```yaml
services:
  bunker:
    image: ghcr.io/kucukkanat/bunker:latest
    container_name: bunker
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - bunker-data:/data            # database, uploads and keys: back this up
    environment:
      BUNKER_URL: ${BUNKER_URL:-}
      # BUNKER_SETUP_CODE: change-me
      # BUNKER_LOG_FORMAT: text
volumes:
  bunker-data:
```

```sh
BUNKER_URL=https://bunker.example.com docker compose up -d
docker compose logs bunker
```

## Compose with automatic HTTPS (Caddy)

`deploy/compose-with-caddy/` runs Bunker behind Caddy, which gets Let's Encrypt certificates for you.

1. Point a DNS record (for example `bunker.example.com`) at the server.
2. Open ports 80 and 443.
3. Start it:
   ```sh
   cd deploy/compose-with-caddy
   DOMAIN=bunker.example.com docker compose up -d
   docker compose logs bunker
   ```

The Caddyfile sets `flush_interval -1` so live updates (server-sent events) aren't buffered. Compose sets `BUNKER_TRUST_PROXY=true` so Bunker reads the real client IP from `X-Forwarded-For`.

## Upgrading

```sh
docker pull ghcr.io/kucukkanat/bunker
docker rm -f bunker && docker run ...   # same command as before
```

Database migrations run automatically at start. Back up the volume first: see [Backups and upgrades](/bunker/docs/admin/backups/).

## Platforms with root-owned disks

Some platforms mount volumes owned by root, which uid 65532 can't write to. Build with `--build-arg BUNKER_RUN_UID=0` (Render and Railway do this through a service variable).
