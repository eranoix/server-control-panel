# contracts/sdui/client-support

Frozen manifests of the SDUI vocabulary, one per versionCode of an Android app
build **already published** to the project's F-Droid repository.

## Why this exists

The app has no forced updates, so an installed build can stay on a device for
months while the server keeps changing. SDUI removes the compile error that would
normally catch a contract change incompatible with an old build. Each file here is
a snapshot of what an app in the field can interpret; `cmd/sdui-compat check`
compares the server's current contract and fixtures against EVERY snapshot and
fails CI if any of them stops holding.

## When to freeze a new manifest

Every time an Android build is **published** to the F-Droid repository, run:

```
go run ./cmd/sdui-compat freeze -version <versionCode>
```

with the exact `versionCode` of the published build. This writes
`android-<versionCode>.json`, the Contract the server generates at freeze time,
i.e. the vocabulary that build was tested against. Do it as part of publishing.

## Append-only

A frozen manifest describes a build that exists in the world; overwriting it
would stop later server changes from being checked against what that build really
knows. `freeze` refuses to overwrite an existing file unless `-force` is passed,
and `-force` is only for fixing a provably wrong freeze before anything depends
on it. Never delete a manifest once its build was published: without forced
updates there is no guarantee nobody still has it installed.

## Reading a `check` failure

```
android-42:
  [breaking] table.components[0]: contracts/sdui/fixtures/screens/x.admin.json: component 0 has type="table" but is missing the required field "rows_source" that the frozen manifest requires
  -> 1 breaking, 0 note(s)

sdui-compat check: FAILED: at least one frozen manifest (an app already
published) does not survive the current server change. Fix the field/type
reported above or revert the change before merging.
```

The first line names the affected manifest (the published versionCode), the
component type or object, and the exact field. Two ways out:

- **The change really is incompatible:** revert it, or put it behind a new
  `sdui_version` that old apps ignore by design.
- **The change looks acceptable:** a `breaking` classification already assumes
  the conservative stance (removing or weakening something an old app depends on
  is always breaking; see `internal/mobilebff/sdui/compat.go`). If it looks wrong,
  the bug is in the classifier (`Compat`/`FixtureRenderable`), not in the gate;
  do not ignore the failure.

A `[note]` (e.g. `field_added_required`) does not fail CI: a new required field
was added that no frozen manifest has. Old apps never send it, so check that the
server copes with its absence.

## Known limit: binary RBAC, not per row

The golden harness (`internal/mobilebff/sdui/golden.go` + `golden_test.go`) only
knows two roles, admin and non-admin. It proves a screen does not leak admin-only
content to a non-admin viewer, but it cannot express per-resource authorization
("user X only sees their own rows"). Screens that need that kind of filter need
their own authorization test.
