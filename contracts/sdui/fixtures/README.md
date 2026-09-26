# SDUI golden fixtures

This folder is the shared corpus of the SDUI contract. The same fixtures are
consumed by two independent sides:

- **Go** (`internal/mobilebff/sdui/contract_test.go`, `TestFixtureConformance`)
  validates each fixture against `contracts/sdui/contract.json`: required fields
  present, no field outside the contract (except in the `unknown-*` fixtures,
  which exist to carry unknown types and fields). This proves **structural**
  conformance only.
- **Go** (`TestFixtureRoundTripMatchesRealMarshaller`, same file) decodes each
  fixture with `UnmarshalScreen` (the server's real read path), re-encodes it with
  `json.Marshal(*Envelope)` (the real `Screen.MarshalJSON`) and compares the
  result **semantically** (canonical JSON) against the file. A hand-written
  fixture that only looks right but differs from the real output (an `omitempty`
  that drops a field, a nested type that encodes differently) fails here even if
  it passes `TestFixtureConformance`.
  **The `unknown-*` fixtures are excluded by design:** they carry a `"type"`
  (e.g. `"gantt"`) or field the server's 7 Go types cannot represent. Tolerating
  that is the **client's** job; `UnmarshalScreen` rejects them, so there is no
  real marshaller output to compare.
- **Kotlin** parses and renders these same fixtures with no server running,
  testing the `:sdui` renderer in isolation.

**Editing a fixture means running both suites again**: the Go one
(`go test ./internal/mobilebff/sdui/...`) and the Kotlin one. A fixture that passes
on only one side promises something the other side does not deliver.

## What each fixture proves

### `all-components.json`
A screen (`fixture.all`) with exactly one component of each of the 7 vocabulary
types, with plausible data: a container table, a notification rule form, a
notification list, a container detail, a "Deploy" action, a CPU chart and a
destructive confirmation for killing a container. It backs the guarantee that the
closed vocabulary exists and is complete.

The `action_id` of the `confirm_destructive` (`container.kill`) is **the same** as
one of the table's `row_actions`: a `confirm_destructive` renders nothing by
itself; it is indexed by `action_id` and looked up when that action fires in
another component.

### `unknown-noncritical.json`
Three components: a known `table`, a `"type": "gantt"` component without the
`critical` key (default `false`), and a known `action`. Tolerance case 1: the two
known components still render; only the unknown type is skipped.

### `unknown-critical.json`
A known `action` plus `{"type": "gantt", "id": "g1", "critical": true}`.
Tolerance case 2: an unknown type with `critical: true` becomes an "update the
app" placeholder in its position, never a silent drop and never a crash.

### `unknown-extra-fields.json`
A valid `table` carrying two extra component-level keys the contract does not
define (`sort_default`, `density`) and one extra key inside a column object.
Tolerance case 4: a newer server adding optional fields must not break an older
client.

### `validation-error.json`
Not a screen: the exact body of a 422, shaped
`{"error": "validation_failed", "fields": {"name": ["required"], ...}}`. The
`form` component maps the `fields` keys straight to its own field `key`s and
renders the errors inline.

## Requirements this corpus covers

- **Closed vocabulary of 7 types:** `all-components.json`.
- **Tolerance to unknown types and fields:** the three `unknown-*` fixtures, one
  per documented tolerance case.
- **Golden corpus testable without a running server:** this whole folder, found
  automatically by `os.ReadDir` in `TestFixtureConformance`, so a new fixture
  needs no test edit.
