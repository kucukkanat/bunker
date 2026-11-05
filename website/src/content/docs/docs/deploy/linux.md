---
title: Linux installer and systemd
description: One command installs Bunker as a hardened systemd service with automatic HTTPS.
sidebar: { order: 3 }
---

```sh
curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sudo BUNKER_URL=https://bunker.example.com sh
```

The installer:

1. Detects your OS and CPU, downloads the matching release from GitHub and **checks its SHA-256** against `checksums.txt`.
2. Installs the binary to `/usr/local/bin/bunker`.
3. Creates a `bunker` system user, the data folder `/var/lib/bunker` and the settings file `/etc/bunker/bunker.env`.
4. Writes and starts a hardened systemd unit (`/etc/systemd/system/bunker.service`).
5. Prints your setup URL and code. The code is also in `/var/lib/bunker/setup-code.txt`.

## Automatic HTTPS

When you pass an `https://` `BUNKER_URL` (and no `BUNKER_LISTEN`), the installer writes `BUNKER_TLS=auto`. Bunker then listens on 443, answers ACME challenges on port 80 and gets Let's Encrypt certificates by itself. You need:

- a DNS record pointing at the server, and
- ports 80 and 443 open to the internet.

Certificates are cached in `/var/lib/bunker/certs`. Set `BUNKER_ACME_EMAIL` to get expiry mail from Let's Encrypt.

Already have a proxy or load balancer? Set `BUNKER_LISTEN=127.0.0.1:8080` instead. See [Behind a proxy](/bunker/docs/deploy/proxy/).

## Options

Pass options after `sh -s --`:

| Option | Environment | |
|---|---|---|
| `--version v1.2.3` | `BUNKER_VERSION` | Install a specific release (default: latest) |
| `--no-service` | `BUNKER_NO_SERVICE=1` | Only install the binary |
| `--dir DIR` | `BUNKER_INSTALL_DIR` | Where to put the binary |
| | `BUNKER_URL`, `BUNKER_LISTEN`, `BUNKER_SETUP_CODE` | Written into `/etc/bunker/bunker.env` on first install |
| | `BUNKER_DOWNLOAD_URL` | A mirror holding the release files and `checksums.txt` (air-gapped installs) |

Without `sudo`, the installer puts just the binary in `~/.local/bin`. To **upgrade**, use Admin → Updates, or run the same command again; your settings file is kept.

## The settings file

```sh
sudo nano /etc/bunker/bunker.env
sudo systemctl restart bunker
journalctl -u bunker -f
```

Every variable is listed in the [configuration reference](/bunker/docs/admin/configuration/).

## What the service is allowed to do

The unit runs as the unprivileged `bunker` user with a tight sandbox:

```ini
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
MemoryDenyWriteExecute=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallFilter=@system-service
SystemCallFilter=~@privileged
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
ReadWritePaths=/var/lib/bunker
UMask=0077
```

It can write only to its data folder and can bind ports 80 and 443 without being root.

## macOS and Windows

- macOS: the same `curl ... | sh` installs the binary. Run `bunker serve`, or `bunker dev` for a single-user local copy without sign-in.
- Windows (PowerShell): `irm https://raw.githubusercontent.com/kucukkanat/bunker/master/install.ps1 | iex`
