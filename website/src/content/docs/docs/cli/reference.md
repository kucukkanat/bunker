---
title: CLI reference
description: The bunker command-line client, built into the same binary as the server.
sidebar: { order: 1 }
---

The client is the same `bunker` binary as the server. Install it with the [installer](/bunker/docs/deploy/linux/) (without `sudo` it only installs the binary into `~/.local/bin`). `bunker help <command>` shows the details of any command.

## Get started

```sh
bunker login https://bunker.example.com   # approve this computer in the browser
bunker deploy                              # publish this folder; creates the app the first time
bunker share ada@example.com "London Office" --role editor
bunker open
```

## Commands

### Start

| Command | |
|---|---|
| `bunker login [url]` | Sign in. Opens the browser to approve this computer (a one-time code is shown in the terminal too) and saves a personal token in `~/.config/bunker/config.json`. `--token bk_...` (or `--token -` for stdin) uses an existing token; `--no-browser`; `--scopes read,write,admin` |
| `bunker deploy [dir\|file\|-]` | Publish a folder, a single file or stdin as a new version. Aliases `publish`, `up` |
| `bunker open [site]` | Open an app (or the dashboard) in the browser. `--draft` opens the draft preview |

`deploy` details:

- The first deploy of a folder creates the app and writes `bunker.json` `{server, site}`. Later deploys update that app and **upload only files that changed** (content hashes). A single file or stdin doesn't write `bunker.json`: pass `--site` to update the same app again (otherwise each deploy creates a new one).
- If `package.json` has a `build` script, Bunker builds first (npm, pnpm, yarn or bun) and uploads `dist/`, `build/` or `out/`. `--build` / `--no-build` force it either way; `--dir dist` picks a folder.
- Files matched by `.gitignore` or `.bunkerignore` are skipped.
- Flags: `--site/-s slug`, `--message/-m text`, `--title/-t name` (new app), `--draft`, `--open/-o`, `--filename` (for stdin, default `index.html`).

```sh
bunker deploy ./site -m "New menu"
bunker deploy report.html --open
cat page.html | bunker deploy -
bunker deploy --draft
```

### Apps

| Command | |
|---|---|
| `bunker sites [ls\|create\|rm\|rename\|info]` | List, create, rename and remove apps. `ls --filter all\|mine\|shared\|org\|starred\|recent\|templates\|trash`, `--query text`; `create`/`rename --slug address`; `create --description text`. Aliases `site`, `ls` |
| `bunker share [site] <email\|group>...` | Share with people (anything with an `@`) or groups (a name or ID). `--role viewer` (default) or `editor` |
| `bunker unshare [site] <email\|group>...` | Stop sharing |
| `bunker versions [site]` | List versions. Alias `history` |
| `bunker rollback [site] [version]` | Make an earlier version live again (default: the previous one). `bunker restore [site] [version]` does the same |
| `bunker pull <site> [dir]` | Download an app's working files into a folder. `--version n`, `--force` |

`[site]` can be left out inside a folder that has a `bunker.json`.

### Agents and automation

| Command | |
|---|---|
| `bunker ai [site] "<prompt>"` | Build a new app by describing it, or change one (`--site`). Changes land in the draft. `--open` |
| `bunker api <op.name> [path params] [--param value ...] [--data json]` | Call **any** API operation |
| `bunker ops [filter]` | List every operation the server offers. `--names`, `--full` |
| `bunker mcp` | Run a local MCP server over stdio ([MCP](/bunker/docs/agents/mcp/)) |
| `bunker init [dir]` | Set up a folder for Bunker and write `AGENTS.md` so coding agents follow the sandbox rules. `--site` links an existing app |

### Account and tools

| Command | |
|---|---|
| `bunker whoami` | Who you're signed in as |
| `bunker logout` | Sign out and revoke this computer's token (`--keep-token` only forgets it) |
| `bunker tokens [ls\|create\|rm]` | Manage personal access tokens: `bunker tokens create ci-deploy --scopes read,write --days 30` |
| `bunker doctor` | Check config, connection, sign-in, versions and clock |
| `bunker update` | Update to the latest release, checksum verified. `--check`, `--force`. Alias `upgrade` |
| `bunker completion bash\|zsh\|fish` | Print a shell completion script |
| `bunker backup [file]` | Download a full backup (database, files, `secret.key`) from the server you're signed in to (admins). `--no-key` leaves the key out. See [Backups](/bunker/docs/admin/backups/) |
| `bunker restore <backup.tar.gz>` | Unpack a backup into `BUNKER_DATA_DIR` (stop the server first; the old database is kept as `bunker.db.pre-restore-<time>`) |

### Server

| Command | |
|---|---|
| `bunker serve` (or no command) | Run the server, configured with [environment variables](/bunker/docs/admin/configuration/) |
| `bunker dev` | A local single-user server on `127.0.0.1:8080` with no sign-in (you are a local admin) |
| `bunker demo` | Like `dev`, pre-filled with example apps, people and data |
| `bunker healthcheck` | Exit 0 when the local server answers `/healthz` (for Docker `HEALTHCHECK`) |
| `bunker version` | Print the version |

## Global flags

`--json` (machine-readable output), `--yes/-y` (skip confirmations), `--server url` (or `BUNKER_SERVER`), `--profile name` (or `BUNKER_PROFILE`, for several logins), `--no-color` (or `NO_COLOR=1`), `--debug` (print HTTP requests), `--help/-h`.

## Every operation is a command

Every server feature is an operation, so new features work in the CLI immediately:

```sh
bunker api sites.list --filter mine
bunker api sites.get team-lunch                  # positional = path params
bunker api sharing.add --site team-lunch --type user --id ada@example.com --role viewer
bunker api files.write --site team-lunch --path about.html --content @about.html
bunker api sites.update team-lunch --data '{"settings":{"spa":true}}'
bunker api db.rule --site team-lunch --coll orders --rule private
bunker api drafts.publish --site team-lunch
```

Numbers and booleans are converted using the parameter's type. `@file` reads a file, `@-` reads stdin, and for upload parameters `@folder` sends a whole folder.

## CI

Skip `login` and set `BUNKER_SERVER` and `BUNKER_TOKEN`:

```yaml
- run: bunker deploy ./dist --site team-handbook -m "$GITHUB_SHA"
  env:
    BUNKER_SERVER: https://bunker.example.com
    BUNKER_TOKEN: ${{ secrets.BUNKER_TOKEN }}
```

Tokens start with `bk_`, have scopes `read`, `write` and optionally `admin`, expire after at most 90 days (`BUNKER_TOKEN_MAX_DAYS`), and appear in the audit log as `via: token`.
