# AGENTS.md — plugin-spice

Standalone plugin repo for the `spice` SPICE-wire check verb (`verb:spice`). The
plugin is a Go module at `candy/plugin-spice/` (module path
`github.com/opencharly/plugin-spice/candy/plugin-spice`); the root `charly.yml`
only declares `discover: candy` so the repo is a project and its candy is
scanned.

Canonical files:

- `candy/plugin-spice/charly.yml` — the `plugin-spice:` candy entity (`plugin:`
  block, `plan:` check).
- `candy/plugin-spice/methods.go` — the `status`/`screenshot`/`cursor`/`click`/
  `mouse`/`type`/`key` methods.
- `candy/plugin-spice/third_party/spice/` — the vendored `Shells-com/spice`
  library (pure Go; cgo audio removed).
- `candy/plugin-spice/schema/spice.cue` — the self-contained `#SpiceInput`.
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the unified Provider model, the out-of-process shape, the per-plugin
  CUE-schema contract, placement. Load before touching the provider or schema.
- `/charly-check:spice` — the `spice:` verb this candy serves.
- `/charly-vm:vm` — the VM/libvirt surface whose display this verb drives.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `go build ./...` in `candy/plugin-spice/` — compile the plugin module.
- `go test ./...` in `candy/plugin-spice/` — the plugin's Go tests
  (`console_test.go`, `record_test.go`, `session_test.go`, `session_wire_test.go`,
  `land_artifact_test.go`).
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema).
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate.
- R10 consumer: a disposable libvirt VM bed whose desktop check composes this
  plugin.

## Modify this repo

- Edit the `plugin-spice:` candy entity, the Go source, and `schema/spice.cue`
  **together** — the schema is the single source for the `params/` struct, so a
  field change not mirrored in the schema desyncs the generated types.
- Keep the vendored `third_party/spice` pure Go (no cgo audio deps); the host
  resolves the SPICE endpoint, so the plugin needs no libvirt.

## Landing

- PR-only. Every change lands through a pull request; the org-required
  `charly/pr-validator` validates the diff and body and arms native auto-merge on
  PASS. Direct pushes to `main` are blocked.
- History lives in `CHANGELOG/` (written by `tag-on-merge` at merge time); the PR
  body IS the changelog.
- The authoritative rulebook is the umbrella `AGENTS.md` in
  `opencharly/opencharly` and `charly/AGENTS.md` in the charly repo. Do not
  restate its rules here.
