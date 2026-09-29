# plugin-spice

SPICE-wire display automation for OpenCharly — the `spice:` check verb.

The verb speaks the SPICE remote-desktop protocol against a VM's display: a
handshake/status probe, native-SPICE screenshots, cursor/mouse/click/type/key
input. It is served **out-of-process** — charly's loader fetches this repo,
host-builds the provider binary, and serves it over go-plugin gRPC, so the
vendored `github.com/Shells-com/spice` library (under `third_party/spice`) lives
here, out of charly's core `go.mod`, with its cgo audio channels removed so it is
unambiguously pure Go.

The host owns the go-libvirt VM resolution and any `qemu+ssh://` side tunnel,
pre-resolving the VM's live SPICE endpoint to a dialable address shipped in the
check env — so this plugin needs no libvirt at all.

## What it provides

| Capability | Surface |
|---|---|
| `verb:spice` | the `spice:` check verb — `status`, `screenshot`, `cursor`, `click`, `mouse`, `type`, `key` |

## How to use it

Compose the plugin candy in a check bed whose VM desktop it drives:

```yaml
- '@github.com/opencharly/plugin-spice/candy/plugin-spice:<tag>'
```

Then author the verb in a plan:

```yaml
- check: the SPICE wire handshake completes
  id: spice-status
  spice: status
  stdout: [{contains: ok}]
  context: [runtime]
```

The R10 consumer is a disposable libvirt VM bed whose desktop check composes this
plugin.

## Layout

- `candy/plugin-spice/` — the plugin module: `methods.go` (the 7-method
  surface), `console.go` / `session.go` / `record.go` (the wire + recorder),
  `third_party/spice/` (the vendored library), `schema/spice.cue`,
  `params/cue_types_gen.go`, `cmd/serve/main.go`.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.

## Related

- Owning skill: `/charly-check:spice` — the `spice:` SPICE-wire check verb,
  served out-of-process by this candy.
- `/charly-vm:vm` — the VM/libvirt surface whose display this verb drives.
- `/charly-internals:plugin` — the out-of-process plugin model.
