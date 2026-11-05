<p align="center">
  <img src="brand/logo-wordmark.svg" alt="Bunker" height="56">
</p>

<h3 align="center">Your own private app cloud.<br>For you, your team, or your whole company.</h3>

<p align="center">
  Drop in a file or folder, or describe an app in plain words, and get a private web app you can share with a link.<br>
  Every app is sandboxed. Built-in accounts out of the box; Microsoft Entra ID, Google Workspace or OIDC when you want them.<br>
  One Go binary, one data folder. MIT licensed.
</p>

<p align="center">
  <a href="https://kucukkanat.github.io/bunker/"><b>Website</b></a> ·
  <a href="https://kucukkanat.github.io/bunker/docs/start/quickstart/"><b>60-second quickstart</b></a> ·
  <a href="https://kucukkanat.github.io/bunker/docs/start/introduction/">Docs</a> ·
  <a href="DEPLOY.md">Deploy</a> ·
  <a href="https://kucukkanat.github.io/bunker/docs/security/threat-model/">Security</a>
</p>

---

## Deploy in one click

<p>
  <a href="https://portal.azure.com/#create/Microsoft.Template/uri/https%3A%2F%2Fraw.githubusercontent.com%2Fkucukkanat%2Fbunker%2Fmaster%2Fdeploy%2Fazure%2Fazuredeploy.json"><img src="https://aka.ms/deploytoazurebutton" alt="Deploy to Azure" height="32"></a>
  <a href="https://render.com/deploy?repo=https://github.com/kucukkanat/bunker"><img src="https://render.com/images/deploy-to-render-button.svg" alt="Deploy to Render" height="32"></a>
  <a href="https://railway.com/deploy/bunker"><img src="https://railway.com/button.svg" alt="Deploy on Railway" height="32"></a>
  <a href="https://app.koyeb.com/deploy?type=git&repository=github.com/kucukkanat/bunker&branch=master&builder=dockerfile&name=bunker&ports=8080;http;/"><img src="https://www.koyeb.com/static/images/deploy/button.svg" alt="Deploy to Koyeb" height="32"></a>
</p>

```sh
# Any Linux server: hardened systemd service + automatic HTTPS
curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sudo BUNKER_URL=https://bunker.example.com sh

# Have Go? One command, then look around with example apps, people and data
go install github.com/kucukkanat/bunker/cmd/bunker@latest && bunker demo

# Docker (before the first release, build it: docker build -t ghcr.io/kucukkanat/bunker https://github.com/kucukkanat/bunker.git)
docker run -p 8080:8080 -v bunker:/data ghcr.io/kucukkanat/bunker

# Fly.io
fly launch --from https://github.com/kucukkanat/bunker --copy-config
```

Open the URL, create your admin account, and drop a file. Every option (Koyeb, Coolify, CapRover, Windows, Compose with Caddy, demo-only platforms) is in **[DEPLOY.md](DEPLOY.md)**.

## What you get

- **Anything becomes an app.** HTML, folders, zips and build output; Markdown docs and slides; CSV and Excel table apps; Jupyter notebooks; PDFs; images, video and audio; single-file React, JSX and TSX. Or start from one of 16 templates, or describe it to the AI builder. [What each file type becomes](https://kucukkanat.github.io/bunker/docs/build/drop-anything/).
- **Share like a document.** People, groups or everyone, as viewer or editor. "Ask for access" requests, notifications, view stats.
- **Every change is a version.** Drafts, diffs and one-click restore.
- **A real backend, built in.** `window.bunker` gives pages a database with access rules and live updates, file uploads, per-person storage, live rooms, AI and notifications, all with the viewer's own permissions. Plus Microsoft 365 data and admin-defined connections to internal APIs. When a page isn't enough, drop `api/<name>.js` or `.ts` next to it: [server functions](https://kucukkanat.github.io/bunker/docs/build/server-functions/) run per call in a WebAssembly sandbox, on demand, on a schedule, after writes or from webhooks, with secrets and outbound `fetch`. No build step, no containers.
- **Built for agents.** A CLI (`bunker deploy`, `share`, `rollback`, `ai`, `api`), local and remote MCP (add `https://your-bunker/mcp` to Claude, Cursor or ChatGPT), `/llms.txt`, and every UI action in the API.
- **Safe by design.** Opaque-origin sandbox, WebAssembly-isolated server functions with memory and time caps, signed content tickets, strict CSP plus `Connection-Allowlist`, no keys in pages, secret scanning, an audit log you can search, export and stream to your SIEM. [Threat model](https://kucukkanat.github.io/bunker/docs/security/threat-model/), including what remains.
- **Tiny footprint.** One pure-Go binary with SQLite. Back up one folder, or let Bunker write scheduled snapshots. Update from the admin page in one click, with an automatic way back.

## Status

Bunker is young and moving fast. The docs mark everything that isn't finished as **Preview**. See the [roadmap](https://kucukkanat.github.io/bunker/docs/project/roadmap/).

## Develop

```sh
go test ./...
go run ./cmd/bunker dev      # http://localhost:8080, signed in as a local admin
go run ./cmd/bunker demo     # same, with example apps and people
go run ./test/perf           # load and profiling harness (see research/PERF.md)
```

- Product plan: [PLAN.md](PLAN.md) · Engineering contract: [CONTRACT.md](CONTRACT.md) · [CHANGELOG](CHANGELOG.md)
- Research and reviews: [research](research/) (deep research, security audit, bug hunt, QA, performance)
- Brand kit: [brand/](brand/) · Website and docs source: [website/](website/) (Astro + Starlight)
- Security reports: [privately via GitHub](https://github.com/kucukkanat/bunker/security/advisories/new)

MIT License.
