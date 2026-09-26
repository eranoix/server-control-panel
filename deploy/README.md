# Deploy contract

`scripts/deploy.sh` does not know any project by name. It runs one contract
(**gate → build → health → symlink → rollback**) parameterised by a
`deploy/<project>.conf` file.

```sh
scripts/deploy.sh --conf deploy/painel.conf <new-binary>   # deploy
scripts/deploy.sh --conf deploy/painel.conf --rollback     # back to the previous one
scripts/deploy.sh --conf deploy/painel.conf --validar      # validate the conf only
```

Without `--conf` the default is `deploy/painel.conf`. The file must still exist:
a default is not permission to run without configuration.

## Two rules

**1. `agentctl` runs the `deploy.sh` of the MAIN working tree** (`$ROOT`). Editing
the script in a worktree and running `agentctl deploy` from there runs the **old**
script. Integrate before you test.

**2. The contract runs WHERE THE ARTIFACT LANDS, not where it was built.** That is
what an empty `BUILD_CMD` is for: the binary can be built on another machine and
arrive ready. `node-agent` is built on the VPS and lands on another host, and
`deploy.sh` does not need to know about the network for that to work.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | healthy deploy |
| `1` | failed and **rolled back on its own**: the service is up on the previous version |
| `2` or more | broken: invalid configuration, or the rollback failed too |

`1` is deliberate. Callers must not read it as a total failure: it is the
difference between "up on the previous version" and "down".

## Keys

### Required: the script dies naming any missing key

| Key | What it is |
|---|---|
| `PROJ_SRC` | where the code is (build worktree) |
| `PROJ_ROOT` | where the artifact lands; **may differ** from `PROJ_SRC` |
| `BIN_DIR` | directory of the stamped binaries |
| `LINK` | the symlink swapped atomically |
| `ARTIFACT_PREFIX` | prefix used by retention and by the rollback lookup |
| `HEALTH_MODE` | `url` or `cmd` |
| `HEALTH_TRIES` · `HEALTH_INTERVAL` | the health window is `TRIES × INTERVAL` seconds |
| `SERVICE` | main unit (`restart` + `reset-failed`) |
| `KEEP_BINARIES` | retention, **minimum 2** (see below) |
| `LOCK_FILE` · `LOG` | `flock` lock and deploy log |

`HEALTH_MODE=url` requires `HEALTH_URL`; `HEALTH_MODE=cmd` requires `HEALTH_CMD`.

**`KEEP_BINARIES < 2` is a HARD error, not a warning.** Rollback needs at least
two previous binaries; finding that out with a broken binary live is too late.

### Optional: empty has a declared meaning

| Key | Empty means |
|---|---|
| `BUILD_CMD` | **project WITHOUT a build step**: the artifact arrives ready. A first-class case |
| `INV_FILE` | no textual invariant pin |
| `STATE_BACKUP_DIR` / `STATE_FILES` | skip state backup rotation (panel specific) |
| `DRAIN_URL` | no queue drain, and therefore **no `jq` dependency** |
| `PREFLIGHT_CMDS` | no project-specific preflight |
| `BIN_CHECK_ARG` | no self-test of the new binary before the swap |
| `EXTRA_ARTIFACTS` | no extra artifacts applied after health |
| `EXTRA_SERVICES` | no extra services restarted after health |

Other defaults: `KEEP_STATE_BACKUPS=10`, `DRAIN_TRIES=60`, `DRAIN_INTERVAL=3`,
`HEALTH_CMD_TIMEOUT=10`.

### `EXTRA_ARTIFACTS`

One line per artifact, `source|destination|optional-service`. Applied **after**
health passes, so a broken build never overwrites a working tool. `$BUILD_DIR` is
available in the conf and points to the new binary's directory.

## `HEALTH_MODE=cmd`

The right probe for a project may not be a GET: it can be a self-test that
exercises the real path (`--selftest`), while a GET on `/healthz` only proves the
process answered. Some projects have no HTTP surface at all.

Each attempt runs under `timeout $HEALTH_CMD_TIMEOUT` and is logged, so a health
gate that approves by mistake can be told apart from one that approves correctly.

## Not part of the contract

Advancing the canonical branch, the `.claude/coord/` board and propagation between
worktrees stay in `agentctl`. They are multi-session coordination for the panel's
repository, not deploy.

## Pins

`scripts/tests/deploy-conf.sh`: assertions on a missing conf, a missing required
key, an invalid `HEALTH_MODE`, `KEEP_BINARIES < 2`, an empty `BUILD_CMD`, and
`HEALTH_MODE=cmd` both ways. It includes a **negative control**: a complete valid
conf must pass. They run inside `agentctl gate` and never touch a real service:
they use `--validar`, which exits before the first side effect.

## Coverage per project

Three projects use this contract and **none of them exercises all of it**:

| Part of the contract | panel | node-agent | tl-agent |
|---|---|---|---|
| **Build** step | ✅ (in `agentctl`) | ✅ Go, on the VPS | ❌ Python, nothing to build |
| Empty `BUILD_CMD` | ✅ | ✅ (build is an earlier step) | ✅ |
| Health by **URL** | ✅ | ✅ | ❌ |
| Health by **COMMAND** | ❌ | ❌ | ✅ |
| Crossing **to another machine** | ❌ (local) | ✅ | ✅ |
| Symlink swap of a **binary** | ✅ | ✅ | ❌ |
| Symlink swap of a **script** | ❌ | ❌ | ✅ |
| **Auto-rollback** on the three signals | — | ✅ | ✅ |
| `EXTRA_SERVICES` | ❌ (uses `EXTRA_ARTIFACTS`) | ❌ | ✅ (`tl-daemon`) |
| State backup / queue drain | ✅ | ❌ | ❌ |

Not covered by any of them: `INV_FILE` and `BIN_CHECK_ARG` outside the panel.

### Single-artifact scope

The contract swaps **one file** atomically (`install -m 0755`). For the panel and
`node-agent` that is the whole program. For `tl-agent` it is only the entrypoint:
its support files travel with a checked hash but stay **outside the atomic swap
and the rollback**. A deploy that rolls back restores the previous entrypoint
while the support files stay on the new version.

### `LINK` may live outside `BIN_DIR`

`swap_symlink` resolves the target relative to where `LINK` lives, so a `LINK` at
the project root pointing into a versions subdirectory works. A relative target
computed from `BIN_DIR` would point at a nonexistent sibling; this is pinned in
`scripts/tests/deploy-conf.sh`.
