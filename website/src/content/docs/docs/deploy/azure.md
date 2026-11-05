---
title: Azure VM
description: Deploy Bunker to an Ubuntu VM with a managed data disk and automatic HTTPS in your own Azure subscription.
sidebar: { order: 4 }
---

[**Deploy to Azure**](https://portal.azure.com/#create/Microsoft.Template/uri/https%3A%2F%2Fraw.githubusercontent.com%2Fkucukkanat%2Fbunker%2Fmaster%2Fdeploy%2Fazure%2Fazuredeploy.json)

The template (`deploy/azure/main.bicep`, compiled to `azuredeploy.json`) creates, in a resource group you choose:

- an **Ubuntu 24.04 LTS VM**,
- a **managed data disk** mounted at `/var/lib/bunker` (formatted once, never reformatted),
- a static public IP with a free DNS name `https://<label>.<region>.cloudapp.azure.com`,
- a network security group that allows ports **80 and 443** only (SSH is off unless you turn it on).

On first boot, cloud-init runs the [Linux installer](/bunker/docs/deploy/linux/) with Bunker listening on `127.0.0.1:8080`, and puts **Caddy** in front for HTTPS (Let's Encrypt).

## Parameters

| Parameter | Default | |
|---|---|---|
| `name` | `bunker` | Prefix for the VM and its resources |
| `location` | the resource group's region | |
| `dnsLabel` | `bunker-<unique>` | Bunker will be at `https://<label>.<region>.cloudapp.azure.com` |
| `vmSize` | `Standard_B2s` | Also `Standard_B1ms` (trying it out), `B2ms`, `D2s_v5`, `D4s_v5`. B2s comfortably serves a few thousand people |
| `dataDiskSizeGiB` | `32` | |
| `adminUsername` | `bunkeradmin` | |
| `authenticationType` | `password` | or `sshPublicKey` |
| `adminPasswordOrKey` | | Required |
| `allowSsh` | `false` | Prefer Azure Bastion, the serial console or Run Command |
| `setupCode` | generated | One-time code for the setup page |
| `bunkerVersion` | `latest` | e.g. `v1.2.0` |

## Outputs

After the deployment finishes (*Deployment → Outputs*):

| Output | |
|---|---|
| `url` | Bunker's address |
| `setupUrl` | Open this and enter the code (give it a few minutes after deployment) |
| `setupCode` | The one-time code |
| `entraRedirectUri` | The redirect URI to register if you add [Entra sign-in](/bunker/docs/sign-in/entra-manual/) |
| `ssh` | SSH command, when `allowSsh` is on |

## Using your own domain

1. Point a DNS record at the VM's public IP.
2. Edit `/etc/bunker/bunker.env` and set `BUNKER_URL=https://bunker.yourcompany.com`.
3. Edit `/etc/caddy/Caddyfile` and replace the `cloudapp.azure.com` name with your domain.
4. `sudo systemctl restart bunker caddy`

## Container Apps (evaluation only)

`deploy/azure/container-apps/` deploys Bunker to Azure Container Apps. It uses the container's local disk, so **data is lost on restart**. Use it to look around, not for real work. Never point SQLite at an Azure Files share.
