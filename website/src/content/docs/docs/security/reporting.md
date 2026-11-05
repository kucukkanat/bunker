---
title: Reporting a vulnerability
description: How to report a security problem in Bunker privately.
sidebar: { order: 4 }
---

Please **don't open a public issue** for security problems.

Report privately through GitHub: [**Security → Report a vulnerability**](https://github.com/kucukkanat/bunker/security/advisories/new) on the Bunker repository.

Helpful to include:

- the Bunker version (`bunker version`, or `GET /healthz`),
- the browser and version, if it involves the sandbox,
- steps to reproduce, ideally a minimal app that shows it,
- what an attacker could do with it.

What to expect:

- an acknowledgement within a few days,
- a fix and a published advisory with credit (if you want it) once a release is out,
- no legal action against good-faith research that respects people's data and stays on your own installation.

Especially interesting: anything that lets app code reach the Bunker session, another app's data, the API outside page-safe operations, or the network; ticket forgery; ways around sign-in restrictions; SSRF.
