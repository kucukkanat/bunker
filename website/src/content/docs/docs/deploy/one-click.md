---
title: One-click deploys
description: Every way to run Bunker, from a button in your cloud console to one line on a Linux server.
sidebar: { order: 1 }
---

Bunker is one program and one data folder. Pick a button, wait a couple of minutes, open the URL, and create your admin account.

## Buttons

| | Where | Keeps your data? | Guide |
|---|---|---|---|
| [Deploy to Azure](https://portal.azure.com/#create/Microsoft.Template/uri/https%3A%2F%2Fraw.githubusercontent.com%2Fkucukkanat%2Fbunker%2Fmaster%2Fdeploy%2Fazure%2Fazuredeploy.json) | A small Ubuntu VM with a managed data disk, a free `*.cloudapp.azure.com` name and HTTPS | Yes | [Azure VM](/bunker/docs/deploy/azure/) |
| [Deploy to Render](https://render.com/deploy?repo=https://github.com/kucukkanat/bunker) | Web service + 5 GB disk (Starter plan or higher) | Yes | [Render, Railway, Fly](/bunker/docs/deploy/paas/) |
| [Deploy on Railway](https://railway.com/deploy/bunker) | Service + volume at `/data` | Yes | [Render, Railway, Fly](/bunker/docs/deploy/paas/) |
| `fly launch --from https://github.com/kucukkanat/bunker --copy-config` | Fly.io: one machine + volume | Yes | [Render, Railway, Fly](/bunker/docs/deploy/paas/) |
| [Deploy to Koyeb](https://app.koyeb.com/deploy?type=docker&image=ghcr.io/kucukkanat/bunker:latest&name=bunker&ports=8080;http;/) | Koyeb | Only after you attach a volume at `/data` | |
| [DigitalOcean App Platform](https://cloud.digitalocean.com/apps/new?repo=https://github.com/kucukkanat/bunker/tree/master) | App Platform | **No, demo only**: data resets on every deploy. For real use, a Droplet + the [Linux installer](/bunker/docs/deploy/linux/) | |
| [Heroku](https://heroku.com/deploy?template=https://github.com/kucukkanat/bunker) | Heroku | **No, demo only**: data resets daily | |
| [Google Cloud Run](https://deploy.cloud.run/?git_repo=https://github.com/kucukkanat/bunker) | Cloud Run | **No, demo only** | |

Self-hosting platforms: **Coolify** (`deploy/coolify/docker-compose.yml`) and **CapRover** (one-click template `deploy/caprover/bunker.yml`).

:::caution[One instance, one real disk]
Bunker keeps everything in SQLite on a local disk. Run **exactly one instance**, on a real block disk. Never put the data folder on a network share like Azure Files, SMB or NFS: SQLite needs real file locks.
:::

## Commands

```sh
# Any Linux server: hardened systemd service, automatic HTTPS for an https URL
curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sudo BUNKER_URL=https://bunker.example.com sh

# Docker
docker run -p 8080:8080 -v bunker:/data ghcr.io/kucukkanat/bunker
```

Windows (PowerShell): `irm https://raw.githubusercontent.com/kucukkanat/bunker/master/install.ps1 | iex`

More detail: [Docker and Compose](/bunker/docs/deploy/docker/), [Linux installer and systemd](/bunker/docs/deploy/linux/).

## After you deploy

1. **Open the URL.** Azure shows it under *Deployment → Outputs*. Render, Railway, Fly and Koyeb show it on the service page.
2. **Find the setup code.** It's printed in the logs when Bunker starts for the first time:
   ```
   ┌──────────────────────────────────────────────┐
   │  Bunker is ready to set up.                  │
   │                                              │
   │  Open:  https://bunker.example.com/setup     │
   │  Code:  BKRT-WXQP                            │
   └──────────────────────────────────────────────┘
   ```
   - **Azure:** *Outputs → setupCode*
   - **Render:** *Environment → `BUNKER_SETUP_CODE`*
   - **Linux installer:** printed at the end, and in `/var/lib/bunker/setup-code.txt`
   - **Everywhere else:** the service logs

   Opening `/setup` from the machine itself (localhost) doesn't need the code.
3. **Create your account.** Choose **Just me or my team**, enter your email, name and a password. You're the admin. [Invite people](/bunker/docs/sign-in/built-in/) with one-time links.
4. **Optional: company sign-in.** Connect [Entra ID](/bunker/docs/sign-in/entra-auto/), [Google Workspace](/bunker/docs/sign-in/google/) or [OIDC](/bunker/docs/sign-in/oidc/), then turn built-in accounts off if you want.

Health check: `GET /healthz` (inside the container: `bunker healthcheck`). Every setting is in the [configuration reference](/bunker/docs/admin/configuration/).
