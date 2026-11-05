<img src="brand/logo-wordmark.svg" alt="Bunker" height="40">

# Deploy Bunker

Bunker is one small program and one data folder. Pick a button, wait a couple of minutes, open the URL, type the setup code. That's it.

## One click

| | Where | Data is kept? | |
|---|---|---|---|
| [![Deploy to Azure](https://aka.ms/deploytoazurebutton)](https://portal.azure.com/#create/Microsoft.Template/uri/https%3A%2F%2Fraw.githubusercontent.com%2Fkucukkanat%2Fbunker%2Fmaster%2Fdeploy%2Fazure%2Fazuredeploy.json) | **Azure** (your own subscription): a small Linux VM with a managed data disk, a free `*.cloudapp.azure.com` name and automatic HTTPS | Yes | **Recommended for Microsoft 365 companies** |
| [![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/kucukkanat/bunker) | **Render**: web service + 5 GB persistent disk (Starter plan or higher) | Yes | |
| [![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/bunker) | **Railway**: service + volume at `/data` | Yes | |
| `fly launch --from https://github.com/kucukkanat/bunker --copy-config` | **Fly.io**: one machine + volume (see [`fly.toml`](fly.toml)) | Yes | |
| [![Deploy to Koyeb](https://www.koyeb.com/static/images/deploy/button.svg)](https://app.koyeb.com/deploy?type=docker&image=ghcr.io/kucukkanat/bunker:latest&name=bunker&ports=8080;http;/) | **Koyeb** | Only after you attach a volume at `/data` in the service settings | |
| [![Deploy to DO](https://www.deploytodo.com/do-btn-blue.svg)](https://cloud.digitalocean.com/apps/new?repo=https://github.com/kucukkanat/bunker/tree/master) | DigitalOcean App Platform | **No, demo only (data resets on every deploy)** | For real use: a Droplet + the installer below |
| [![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://heroku.com/deploy?template=https://github.com/kucukkanat/bunker) | Heroku | **No, demo only (data resets daily)** | |
| [![Run on Google Cloud](https://deploy.cloud.run/button.svg)](https://deploy.cloud.run/?git_repo=https://github.com/kucukkanat/bunker) | Google Cloud Run | **No, demo only (data resets)** | |

Self-hosting platforms: **Coolify** ([`deploy/coolify/docker-compose.yml`](deploy/coolify/docker-compose.yml)) and **CapRover** (one-click template [`deploy/caprover/bunker.yml`](deploy/caprover/bunker.yml), or `caprover deploy` from this repo).

> Bunker stores everything in SQLite on a local disk. That's why it runs as **exactly one instance**, and why it needs a real disk, not a network share like Azure Files or NFS.

## Docker in 3 lines

```sh
docker volume create bunker-data
docker run -d --name bunker -p 8080:8080 -v bunker-data:/data --restart unless-stopped ghcr.io/kucukkanat/bunker
docker logs bunker   # → open the URL it prints and enter the setup code
```

No image published yet, or you want the latest code? Build it straight from GitHub (no clone needed):

```sh
docker build -t ghcr.io/kucukkanat/bunker https://github.com/kucukkanat/bunker.git
```

Or `docker compose up -d` with [`docker-compose.yml`](docker-compose.yml). Want HTTPS on your own domain? Use [`deploy/compose-with-caddy/`](deploy/compose-with-caddy/): `DOMAIN=bunker.example.com docker compose up -d`.

## Any Linux server (or a Mac)

```sh
curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sudo sh
```

It downloads the right binary, checks its SHA-256 checksum, installs it as a hardened systemd service (user `bunker`, data in `/var/lib/bunker`, settings in `/etc/bunker/bunker.env`), starts it and prints your setup URL and code.

- Without `sudo`, it installs just the binary into `~/.local/bin`.
- `... | sh -s -- --version v1.2.3` pins a version. `--no-service` skips the service.
- Run it again to upgrade.

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/kucukkanat/bunker/master/install.ps1 | iex
```

Just want to look around? `bunker demo` starts Bunker on your laptop with example apps, people and data. `bunker dev` runs Bunker with a single local admin and no sign-in. Opening the setup page from the same machine (localhost) skips the setup code.

## After you deploy

1. **Open the URL.** Azure shows it under *Deployment → Outputs*. Render, Railway, Fly and Koyeb show it on the service page.
2. **Enter the setup code.** It's printed in the logs when Bunker starts for the first time:
   ```
   ┌──────────────────────────────────────────────┐
   │  Bunker is ready to set up.                  │
   │  Open:  https://bunker.example.com/setup     │
   │  Code:  ABCD-EFGH                            │
   └──────────────────────────────────────────────┘
   ```
   - **Azure:** *Outputs → setupCode*
   - **Render:** *Environment → BUNKER_SETUP_CODE*
   - **Linux installer:** printed at the end, also in `/var/lib/bunker/setup-code.txt`
   - **Everywhere else:** the service logs
3. **Create your account.** Pick **Just me or my team** and enter your email, name and a password. You're the admin. Invite others from *Admin → People*: each invite is a one-time link you send them.
4. **Optional: company sign-in.** Want coworkers to use their work account? In *Admin → Sign-in* choose Microsoft Entra ID (it can register itself in your tenant), Google Workspace or any OIDC provider. Afterwards you can turn off built-in accounts.

Done. Drop a file on the home page and you have your first app.

## Settings

All settings are environment variables. Usually you don't need any.

| Variable | Default | |
|---|---|---|
| `BUNKER_URL` | auto-detected on Render, Railway, Fly, Koyeb, Heroku, Azure App Service, DigitalOcean | Public URL, e.g. `https://bunker.corp.com`. Set it when you use your own domain |
| `PORT` / `BUNKER_LISTEN` | `:8080` | Where Bunker listens (plain HTTP; put TLS in front) |
| `BUNKER_DATA_DIR` | `/data` if it exists, else `./bunker-data` | Database, files and keys. **Back this up** |
| `BUNKER_SETUP_CODE` | generated | Fixed first-run setup code |
| `BUNKER_TRUST_PROXY` | on behind a platform proxy | Trust `X-Forwarded-For` |
| `BUNKER_LOG_FORMAT` | `json` | `text` for human-readable logs |

Health check: `GET /healthz` (in the container: `bunker healthcheck`).

---

<details>
<summary>Notes for maintainers</summary>

- **Releases:** push a tag `vX.Y.Z`. [`release.yml`](.github/workflows/release.yml) publishes the binaries + `checksums.txt` to GitHub Releases and a multi-arch image to `ghcr.io/kucukkanat/bunker`. After the first release, make the GHCR package **public** (Package settings → Change visibility), or the image-based deploys can't pull it.
- **Azure:** edit [`deploy/azure/main.bicep`](deploy/azure/main.bicep) (+ [`cloud-init.yaml`](deploy/azure/cloud-init.yaml)), then `az bicep build -f deploy/azure/main.bicep --outfile deploy/azure/azuredeploy.json` and commit both. [`deploy/azure/container-apps/`](deploy/azure/container-apps/) is an evaluation-only Container Apps variant (local ephemeral disk; data is lost on restart).
- **Railway:** the button needs a published template with code `bunker`. Create it in the Railway template composer from this repo: add a volume mounted at `/data` and the variable `BUNKER_RUN_UID=0` (Railway volumes are owned by root), then publish. [`railway.json`](railway.json) sets the Dockerfile builder and the health check.
- **Root-owned disks:** the image runs as uid 65532. Render and Railway mount disks owned by root, so they build with `BUNKER_RUN_UID=0` (a Docker build arg; Render and Railway pass service variables to the build).
- **Heroku / Cloud Run** share [`app.json`](app.json) ([`heroku.yml`](heroku.yml) for Heroku's container stack).
</details>
