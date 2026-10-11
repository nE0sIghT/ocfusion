# Module development - API 1

This document describes the implemented OCFusion 0.3 module contract. Modules are separate trusted executables, not Go plugins. Install them through the router's administrator SSH account. They run with administrator privileges; resource/capability declarations are not a security sandbox.

## Package

A gzip-compressed tar archive contains files directly at its root:

```text
module.json
bin/module
ui/index.html
```

Bundled modules use the same package structure under `modules/<id>/` in the OCFusion distribution. Each directory has its own `module.json`, entrypoint and optional UI/resources. Independently installed module archives keep the package files directly at their archive root.

Only regular files and directories are accepted. Absolute/traversal paths, symlinks and device nodes are rejected. The compressed input is read with a 64 MiB limit, expanded regular-file content is limited to 128 MiB and at most 10,000 entries are accepted. Individual files also have size bounds. Include your own license and all required runtime files; packages should not rely on the router providing a general Linux userspace.

Example manifest:

```json
{
  "schema": 1,
  "id": "heartbeat",
  "name": "Heartbeat example",
  "version": "1.0.0",
  "module_api": 1,
  "entrypoint": "bin/module",
  "ui": "ui/index.html",
  "requires": {
    "architecture": "aarch64",
    "models": ["GT-BE98"],
    "platform_api": 1
  },
  "actions": [],
  "capabilities": [],
  "resources": {}
}
```

| Field | Meaning |
| --- | --- |
| `schema`, `module_api` | Must be 1 |
| `id` | Stable ID matching `[a-z][a-z0-9-]{0,31}` |
| `name` | Display name |
| `version` | `[a-zA-Z0-9][a-zA-Z0-9._-]{0,47}`; new bytes require a new version |
| `entrypoint` | Relative executable path within the package |
| `ui` | Optional relative HTML path; its directory is published for UI assets |
| `requires` | Optional architecture, supported models and platform API requirement |
| `actions` | Optional additional fixed operation names matching `[a-z][a-z0-9-]{0,31}` |
| `capabilities` | Descriptive metadata; does not grant or limit privileges |
| `provides_api` | Optional positive provider API version |
| `dependencies` | Required module IDs and exact compatible provider API versions, for example `[{"id":"vpn","api":1}]` |
| `install` | Opt into the preflight/install hooks for persistent runtime setup |
| `state_paths` | Owned top-level `/jffs/ocfusion*` directories included in distribution update backup and explicit prune; must not include platform state or overlap another module |
| `stock_pages` | Optional stock-page script contributions: supported path `/VPN/vpnc.html`, relative script path within the package |
| `resources` | Map of resource namespace to value; duplicate namespace/value ownership is rejected |

Dependencies must exist, be enabled when their consumer is enabled and provide the requested API. Cycles and incompatible providers are rejected. Providers start before consumers and stop after them; disabling or removing a required provider is rejected. `status` reports process lifecycle. `diagnose` reports module-specific state. Client connect/disconnect operations belong to declared actions, not platform process enable/disable.

A version's approved payload hashes are stored by the manager. Verified bytes are copied to a RAM snapshot before execution. This detects unexpected changes but does not authenticate the package author. There is no signed package repository in this release.

## Lifecycle and IPC

The entrypoint receives one argument, the operation name. Implement:

| Operation | Purpose |
| --- | --- |
| `validate` | Check supplied configuration before install/config update |
| `preflight` | For `install: true`: verify an existing installation before its distribution update; leave it unchanged |
| `install` | For `install: true`: migrate owned state and install persistent runtime helpers idempotently |
| `prepare` | Prepare idempotently before starting |
| `run` | Remain in the foreground until stopped |
| `reload` | Apply committed configuration |
| `remove` | Clean up module-owned resources |
| `diagnose` | Return domain diagnostics |

A declared action is invoked in the same way. One-shot operations receive JSON on stdin:

```json
{"api":1,"operation":"validate","config":{},"config_path":"/jffs/ocfusion-platform/modules/heartbeat/config.json"}
```

`config` may be null. `EXT_MODULE_ID` identifies the module. One-shot execution has a 20-second timeout and a 32 KiB stdout capture. Return exit status 0 and valid JSON, for example `{"ok":true}`. Empty output is treated as `{}`. A nonzero exit status rejects the operation; JSON alone does not determine success. Stderr is not forwarded to the UI. Keep diagnostic JSON small enough to avoid truncation.

A module may publish a public status snapshot without a browser request. The manager supplies `EXT_PUBLIC_STATUS_PATH` to `run`. Atomically replace this private RAM file with at most 16 KiB of valid JSON, using mode 0600. Refresh it at least every five seconds. This is an opt-in interface: do not write private configuration, credentials, URLs containing secrets, raw errors or request replies. Use an explicit projection of safe fields. The platform validates size, JSON and freshness, but cannot infer which fields are secret.

The manager publishes a copy at `/user/extensions-status/<id>.json` through the stock ASUS web server. The envelope contains `module`, process `state`, `updated_at`, `data_updated_at` and `data`. `data` is null when the worker is stopped, the snapshot is missing/invalid or its file has not been refreshed for 15 seconds. This is the same mechanism for shipped and independently installed modules. A per-profile producer should also reject stale source snapshots instead of refreshing old data indefinitely.

`run` receives `{"api":1,"operation":"run"}` on stdin. `EXT_CONFIG_PATH` points to the private configuration. The manager owns the process group; stop sends TERM, then KILL after five seconds. Handle TERM and clean up your children. Keep the process alive in the foreground. The manager detects early exit during startup and restarts unexpected exits with backoff. General status values include RUNNING, STOPPED, RESTARTING and WAIT_STORAGE_OR_DEPENDENCY. Stdout is bounded; a streaming NDJSON status protocol is not interpreted.

Install uses staging and validate/prepare before replacing the previous version. Startup failure restores the previous version where possible. Same version/same hashes is idempotent; same version/different bytes is rejected. Configuration updates validate, atomically save a mode-0600 JSON file and invoke reload. Reload failure restores the prior configuration. Generic configuration is whole-object replacement, not field-wise secret PATCH.

## Storage and commands

Use `extctl` to install and manage your module:

```sh
/jffs/ocfusion-platform/extctl install /tmp/heartbeat-1.0.0.tar.gz
/jffs/ocfusion-platform/extctl list
/jffs/ocfusion-platform/extctl status heartbeat
/jffs/ocfusion-platform/extctl config heartbeat /tmp/config.json
/jffs/ocfusion-platform/extctl diagnose heartbeat
/jffs/ocfusion-platform/extctl disable heartbeat
/jffs/ocfusion-platform/extctl enable heartbeat
/jffs/ocfusion-platform/extctl remove heartbeat
```

`extctl` is a symlink to extd. CLI failures return a nonzero exit status.

## Module UI

Pages are published under `/user/extensions-modules/<id>/...` and opened in the common panel's module iframe. Use relative paths for your own assets. ASUS supplies login and the common CSS; do not bundle firmware files.

This is the same contract for modules shipped in the distribution and independently installed packages. The manager generates menu entries and overview cards from module metadata. There are no build-time HTML/JavaScript slots or module-ID exceptions in UI publishing. Existing `#<id>` links remain aliases for `#module-<id>`.

An iframe can call:

```js
await parent.EXTENSIONS.action('heartbeat', 'enable');
await parent.EXTENSIONS.action('heartbeat', 'disable');
// Public status polling does not use the command mailbox.
const snapshot = await parent.EXTENSIONS.status('heartbeat');
const diagnostics = snapshot.data;
// An on-demand diagnostic action may still use the mailbox.
await parent.EXTENSIONS.action('heartbeat', 'diagnose');
// Custom actions must be declared and implemented by the module.
const settings = await parent.EXTENSIONS.action('heartbeat', 'settings');
await parent.EXTENSIONS.call('heartbeat', 'update-settings', {interval: 10});
await parent.EXTENSIONS.configure('heartbeat', {interval: 10});
```

`status(id)` reads a public snapshot and checks its envelope freshness. It does not serialize with command transactions or write NVRAM. Treat null data as unavailable, never as a successful tunnel state. `action(id, operation)` returns the operation's JSON response. Standard browser operations are `enable`, `disable`, `status` and `diagnose`; other operations must be declared in the manifest's `actions`. `call(id, action, data, onProgress?)` invokes a declared action with JSON data, delivered in the entrypoint's usual stdin envelope as `config`. `configure(id, config, onProgress?)` keeps the validate/save/reload whole-object configuration contract. Optional progress callbacks receive display text. `EXTENSIONS.busy` reports an active transport transaction; calls during another transaction reject rather than overwrite the mailbox.

The browser transport uses the ASUS session, bounded NVRAM mailbox, nonce acknowledgements and chunked uploads. Calls with up to 1024 bytes of JSON use a bounded inline request and work over HTTP as well as HTTPS. Larger calls and configuration uploads require HTTPS for the browser SHA-256 implementation. Configuration/action data is limited to 16 KiB. Browser JSON responses are limited to 2048 bytes after JSON compaction, and must fit the complete 4 KiB ASUS mailbox response; larger responses reject. Entrypoints retain their 32 KiB CLI output bound, so browser-facing actions should return a small object. Requests expire rather than acknowledging a stale status. Entry failure rejects the Promise with a generic message; stderr and raw errors are not exposed.

There is no browser archive installer or automatic secret redaction. A module implements its own settings/read/PATCH actions if needed, declares them in `actions` and returns only safe public fields. Configuration file paths remain private; do not expose secrets in UI assets or diagnostics.

Modules may use `parent.OCF_I18N.language` to follow the panel's `en`/`ru` language. A module can reuse the translation engine by setting `window.OCF_MODULE_TRANSLATIONS` to a dictionary with English phrases as keys and Russian translations as values before loading `/user/ocfusion-i18n.js`. The engine follows the parent language, translates the module document and observes dynamic text. The shared panel stylesheet is `/user/extensions-panel.css`. Use `body class="bg"` and a `main class="ext-shell ext-module"` wrapper to reuse panel controls/colors without nesting the outer header/sidebar.

For SSH, a declared action with data is invoked with `extctl call MODULE ACTION FILE`; omit FILE for a no-data action. This uses the same manager validation and entrypoint envelope as browser calls.

## Runnable example

`docs/examples/heartbeat` includes an executable shell entrypoint and a small bilingual page. It demonstrates lifecycle and UI only, with no networking changes.

```sh
cd docs/examples/heartbeat
tar -czf /tmp/heartbeat-1.0.0.tar.gz module.json bin ui
```

Transfer it through SSH streaming, install with extctl, then open Modules in OCFusion. Disable/enable it and remove it with the commands above. It is not installed automatically. The example's archive can also be tested natively through `meson test -C build/tests --suite example --print-errorlogs`.

## Persistent data ownership

The manager owns `/jffs/ocfusion-platform/modules/<id>/config.json`. Modules must not modify another module's configuration or the platform registry. Use `/jffs/ocfusion-platform/modules/<id>/data/` for additional persistent module data; the module owns its contents and migrations. The `remove` operation cleans runtime resources and retains configuration and persistent data. Platform uninstall retains these directories by default; `install.sh --uninstall --prune` deletes them after all additional modules have been removed. Keep persistent data within these owned directories so the platform can prune it without interpreting module contents.

If your module needs a separate persistent directory, declare it in `state_paths` as described below. Undeclared directories are not included in automatic backup or pruning.

## VPN provider API 1

The `vpn` module provides API 1. A client declares `dependencies: [{"id":"vpn","api":1}]`. Connections belong to a stable pair of module ID and client profile ID. Each receives an independent native Fusion binding, interface, routing table and rule priority. OpenConnect and other consumers use the same contract.

Send one JSON object per connection to `/tmp/ocfusion-vpn/control.sock`, mode 0600. Requests are bounded to 16 KiB. This is a trusted administrator integration boundary, not a sandbox. All requests include `api: 1`, `client` and `profile`; IDs match `[a-z][a-z0-9-]{0,31}`.

| Operation | Effect |
| --- | --- |
| `list` | List connections owned by the requesting module |
| `claim` | Create or retrieve the connection for the owner pair; repeated requests are idempotent |
| `state` | Return connection resources and assigned networks |
| `prepare` | Ensure native policy and guards are ready |
| `apply` | Apply normalized tunnel parameters and renew this connection's heartbeat |
| `down` | Close this connection's forwarding, retaining its guards |
| `release` | Remove the connection after its native Wi-Fi/VLAN assignments are removed |

`claim` returns `manifest.id`, `interface`, `unit`, `vpnc_idx`, `table`, `priority` and `name`. Subsequent requests must include that ID as `connection`. A different module or profile cannot operate on it. Capacity is limited by available native slots and network resources; allocation failure must be handled by the client.

```json
{
  "api": 1,
  "client": "example-vpn-client",
  "profile": "office",
  "connection": "connection-8",
  "operation": "apply",
  "tunnel": {
    "interface": "ocvpn1",
    "address": "192.168.199.151",
    "mtu": 1426,
    "dns": ["8.8.8.8"]
  }
}
```

Use the interface returned by the provider rather than a fixed name. Responses contain `ok`, connection `manifest`, `networks` and selected `dns`. Failures return `ok: false` and `error`; redact errors before exposing them to the browser. Credentials never go to the provider.

Renew each active connection with `apply` at least every 3 seconds. After 12 seconds without a heartbeat the provider closes that connection while keeping its guards. A bridge cannot belong to multiple connections. Remove native network assignments before `release`. Releasing a connection never removes the platform mailbox reservation.

OpenConnect settings actions use `{ "profile": "office", "data": {...} }` as the module action configuration. `profiles` lists IDs; `add-profile` and `delete-profile` create/remove profiles; `settings`, `update-settings`, `import-profile`, `diagnose`, `connect`, `disconnect` and `reconnect` target the selected profile. The default profile preserves the 0.2.9 installation and cannot be deleted individually.

## Persistent installation helpers

Set `install: true` only when your module needs persistent runtime helpers or migration beyond its package directory. Implement two additional operations:

- `preflight` verifies the previous installation without changing it. Exit nonzero to reject an unsafe update.
- `install` installs helpers and migrates owned data. It must be idempotent and preserve saved configuration.

Declare owned top-level `/jffs/ocfusion*` directories in `state_paths`. Do not claim the platform directory or another module's state. These directories participate in update rollback and explicit platform prune. Ordinary generic configuration remains in the module's platform configuration directory.

Entrypoints receive `EXT_PACKAGE_PATH`, the persistent package location, and `EXT_PAYLOAD_PATH`, the verified invocation payload. Use the latter for executable inputs copied during installation. Use the former when recording a persistent package reference. Default removal must retain configuration; refuse removal while external resources still depend on your module.

## Scripts on stock ASUS pages

Use `stock_pages` to add a script to a supported stock page through the platform's existing overlay. Declare a relative script path within your package. Stock-page contributions do not require a module UI:

```json
{
  "stock_pages": [
    {
      "path": "/VPN/vpnc.html",
      "script": "ui/fusion.js"
    }
  ]
}
```

Currently only `/VPN/vpnc.html` is supported. Contributions are loaded for enabled modules. API-only modules omit `ui`; they appear in the module inventory but have no menu entry or overview card. Make your script idempotent, preserve stock controls and use the existing authenticated server. Link to your module page with `/user/extensions.html#<module-id>`; use `target="_top"` when the stock page is an iframe. Do not patch the shared header or publish a separate listener.
