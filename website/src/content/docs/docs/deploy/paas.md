---
title: Render, Railway and Fly
description: Run Bunker on a platform-as-a-service with a persistent disk.
sidebar: { order: 5 }
---

These platforms build the Dockerfile, give Bunker a persistent disk at `/data`, and terminate HTTPS for you. Bunker detects the public URL from the platform's variables (`RENDER_EXTERNAL_URL`, `RAILWAY_PUBLIC_DOMAIN`, `FLY_APP_NAME`), so usually you set nothing.

## Render

[**Deploy to Render**](https://render.com/deploy?repo=https://github.com/kucukkanat/bunker)

`render.yaml` defines one Docker web service:

- plan `starter` (persistent disks need a paid instance),
- a 5 GB disk `bunker-data` at `/data`,
- health check `/healthz`,
- `BUNKER_SETUP_CODE` generated for you (find it in *Environment*),
- `BUNKER_RUN_UID=0`, because Render's disks are owned by root.

When you add a custom domain, set `BUNKER_URL` to it.

## Railway

[Deploy on Railway](https://railway.com/deploy/bunker): the template builds the Dockerfile with a volume at `/data`, sets `BUNKER_RUN_UID=0` and generates a `BUNKER_SETUP_CODE` (in the service's Variables). Open the generated domain and go to `/setup`.

Or by hand, from your fork:

1. **New project → Deploy from GitHub repo**, pick your fork of Bunker. `railway.json` sets the Dockerfile builder and the health check.
2. **Variables**: add `BUNKER_RUN_UID` = `0` (Railway volumes are owned by root).
3. **Settings → Volumes**: add a volume mounted at `/data`.
4. **Settings → Networking → Generate domain**, then open it. Create your account on the setup page (the setup code is in the deploy logs).

## Fly.io

```sh
fly launch --from https://github.com/kucukkanat/bunker --copy-config
fly logs        # setup URL and code
```

`fly.toml` creates a 1 GB volume `bunker_data` at `/data`, keeps one machine always running (`auto_stop_machines = "off"`, `min_machines_running = 1`), forces HTTPS and checks `/healthz`. Grow the volume with `fly volumes extend`.

:::caution[Keep it at one machine]
SQLite lives on the volume, so Bunker must run as exactly one instance: `fly scale count 1`. Don't add replicas on any platform.
:::

## Platforms that lose data

DigitalOcean App Platform, Heroku and Google Cloud Run have no persistent block disks. Their buttons are fine for a demo, but the database resets. For DigitalOcean, use a Droplet with the [Linux installer](/bunker/docs/deploy/linux/) instead.
