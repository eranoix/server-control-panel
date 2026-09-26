# Prometheus / Alertmanager / Grafana configuration

These files are the source of truth. The running stack uses a copy of them in a
directory on the host that is not under version control, so every change is made
here first and then copied over.

## Publishing a change

Copy the files to the live directory and **recreate** the Prometheus container
(`docker compose up -d --force-recreate prometheus`). Recreating is required:

> The rules file is a **file bind mount**. An editor that writes and renames
> swaps the inode, and the container keeps seeing the old file.
> `POST /prometheus/-/reload` returns **HTTP 200 and loads nothing**. Only
> recreating the container redoes the mount.

After publishing, check that the container reports as many alert rules as the
file defines.

## What lives here

| file | what |
|---|---|
| `prometheus-rules.yml` | the alerts, in three groups: `servercontrolpanel`, `host`, `containers` |
| `alertmanager.yml` | routing; the only receiver is the panel's own loopback webhook (`/_internal/alert`), with no secret |
| `docker-compose.yml` | prometheus, grafana, node-exporter, cadvisor, alertmanager |

## Swap alerts

`HostMemoryLow` looks at `MemAvailable` and `ContainerMemoryNearLimit` only fires
above 90% of a container's limit, so neither sees a full swap: RAM can be
plentiful, and a container started with `--memory` but no `--memory-swap` may
fill swap while staying well under its limit. The three swap alerts
(`HostSwapAlmostFull`, `HostSwapParado`, `HostSwapThrashing`) plus
`ContainerSwapHeavy` cover that case.
