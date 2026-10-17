# Module development - API 1

This document describes the implemented OCFusion 0.4 module contract. Modules are separate trusted executables, not Go plugins. Install them through the router's administrator SSH account. They run with administrator privileges; resource/capability declarations are not a security sandbox.

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
| `requires` | Optional architecture and platform API requirement |
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
// Public status polling does not use the command transport.
const snapshot = await parent.EXTENSIONS.status('heartbeat');
const diagnostics = snapshot.data;
// An on-demand diagnostic action may still use the command transport.
await parent.EXTENSIONS.action('heartbeat', 'diagnose');
// Custom actions must be declared and implemented by the module.
const settings = await parent.EXTENSIONS.action('heartbeat', 'settings');
await parent.EXTENSIONS.call('heartbeat', 'update-settings', {interval: 10});
await parent.EXTENSIONS.configure('heartbeat', {interval: 10});
```

`status(id)` reads a public snapshot and checks its envelope freshness. It does not serialize with command transactions or write NVRAM. Treat null data as unavailable, never as a successful tunnel state. `action(id, operation)` returns the operation's JSON response. Standard browser operations are `enable`, `disable`, `status` and `diagnose`; other operations must be declared in the manifest's `actions`. `call(id, action, data, onProgress?)` invokes a declared action with JSON data, delivered in the entrypoint's usual stdin envelope as `config`. `configure(id, config, onProgress?)` keeps the validate/save/reload whole-object configuration contract. Optional progress callbacks receive display text. `EXTENSIONS.busy` reports an active transport transaction; calls during another transaction reject rather than overwrite this panel's active request.

The browser transport uses the ASUS session, native `splash_page.cgi` multipart upload into a private RAM file and a RAM JSON reply. Requests and replies contain a random request ID, receiver instance and CRC32/IEEE of their exact UTF-8 payload strings. Requests expire after at most one minute. A bad checksum is discarded before dispatch; a matching HTTP upload response alone is not acknowledgement. Calls/configuration data are limited to 16 KiB and sent in one request, without chunks or browser Web Crypto. Calls up to 1024 bytes work over HTTP and HTTPS; larger calls/configuration require HTTPS to protect private data in transit. Browser JSON action responses remain limited to 2048 bytes after compaction; the CLI output bound remains 32 KiB. Entry failure rejects the Promise with a generic message; stderr and raw errors are not exposed.

The shared inbox/reply can be overwritten by concurrent tabs. Each panel rejects overlapping local transactions, but there is no cross-tab locking or delivery guarantee. A timeout means an unknown outcome, not a confirmed failure; modifying commands are not automatically retried. Modules should use revision checks for configuration updates and idempotent actions where appropriate. CRC32 checks accidental corruption and does not replace ASUS authentication or HTTPS.

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

Send one JSON object per connection to `/tmp/ocfusion-vpn/control.sock`, mode 0600. Requests are bounded to 16 KiB. This is a trusted administrator integration boundary, not a sandbox. Connection requests include `api: 1`, `client` and `profile`; IDs match `[a-z][a-z0-9-]{0,31}`.

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

Renew each active connection with `apply` at least every 3 seconds. After 12 seconds without a heartbeat the provider closes that connection while keeping its guards. A bridge cannot belong to multiple connections. Remove native network assignments before `release`. Releasing a connection removes only its own native binding; web transport has no Fusion reservation.

OpenConnect settings actions use `{ "profile": "office", "data": {...} }` as the module action configuration. `profiles` lists IDs; `add-profile` and `delete-profile` create/remove profiles; `settings`, `update-settings`, `import-profile`, `diagnose`, `connect`, `disconnect` and `reconnect` target the selected profile. The default profile is created during installation and cannot be deleted individually.

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

## Router DNS policy

The VPN provider owns one global router DNS policy. `dns-select` takes the caller's
`client` and `profile` and `prefer_dns: true` to prefer that connection. Setting
`prefer_dns: false` clears only that owner's selection. `dns-state` requires `api`
and `client` and returns `dns_status` with `policy`, `path` (VPN, WAN or ERROR during failed cleanup) and an
optional fallback `reason`. Selection persists across restarts.

A selected connection may operate without SDN assignments. `apply` accepts
`native_dns: true` to retain ASUS DNS for assigned networks instead of redirecting
queries to the tunnel's advertised DNS. The globally selected connection always
uses native DNS. Tunnel DNS is optional in this mode. The provider marks outgoing
native resolver traffic and applies scoped tunnel NAT. Losing the connection
restores WAN DNS independently of forwarding guards on assigned networks.

IPv6 upstream configurations currently use WAN fallback because managed tunnels
carry IPv4. Changes restart stock stubby connections without restarting DHCP.

## Router DNS filter registration

A resolver module declares a dependency on `vpn` API 1 and uses the same private socket with its module ID as `client`. These operations do not need a `profile` or allocate Fusion resources:

| Operation | Effect |
| --- | --- |
| `resolver-info` | Return available IPv4 bridge addresses/CIDRs and the provider-owned socket mark |
| `resolver-register` | Capture router-address TCP/UDP DNS for this owner; include `resolver: {"port": 53535}` |
| `resolver-release` | Restore native DNS capture for this owner |
| `dns-state` | Read the global DNS policy and active path |

Only one filter owns capture at a time. Start listening before registration and renew at least every three seconds; capture expires after twelve seconds without renewal. Release capture before stopping the resolver. Re-register after provider restart. Resources are recovered from private RAM records after an unclean provider exit.

Capture preserves client IP and covers bridge router addresses plus router-local `127.0.0.1:53`. Backend sockets must set the returned single-bit `SO_MARK` so requests to native resolvers bypass capture. The provider steers that mark through the selected IPv4 VPN when available. A resolver must recreate remote upstream sockets when `dns-state` changes, and prevent IPv6 custom upstreams from bypassing an active IPv4 DNS path.

AdGuard Home inherits per-network native DNS using client/CIDR upstream mapping. Its administrator API is available only on a private mode-0600 Unix socket; the module exposes settings and bounded aggregate statistics through normal platform actions. It does not expose the upstream administrator UI or a new HTTP listener.

The module's `catalog` action returns curated DNS filter subscriptions.
`filter-status` returns bounded rule counts, successful update dates and error
flags. `refresh-filters` queues an asynchronous update of saved lists. Downloads
start automatically and repeat every 24 hours while protection is enabled;
failed downloads retain loaded rules. Cache and statistics use RAM and lists
are downloaded again after reboot. Manual list URLs and rules remain available.

## OpenConnect OTP actions

Private profiles accept `otp_mode` (`none`, `manual`, `totp`), `otp_secret` and an optional `otp_field`. Settings responses return only `has_otp_secret`; update-settings supports replace/clear with the usual profile revision check.

A public `WAIT_OTP` snapshot contains only `otp: {"id":"...","expires":1234567890}`. Submit through `submit-otp` using the normal profile action envelope with `data: {"challenge":"...","code":"123456"}`; `cancel-otp` uses the same challenge ID. Codes are bounded and delivered once to the matching live worker over a private control socket and structured client pipes. Never publish a code or token secret in status.

## Wi-Fi radio settings actions

The independent `radio-settings` module uses native ASUS regional selection. It has no VPN dependency. `settings` returns `revision`, `mode`, `phase`, `can_apply` and `profiles`. `apply` accepts `{"revision":0,"profile":"AA"}`; `restore` accepts the current `revision`. Both use normal module action envelopes. The profile catalog includes EU (E0/101), AA (US/102), AU (AU/100), JP (JP/101), KR (KR/100) and XX (US/104). Each entry includes `id`, `country`, `country_revision` and `available`; only pairs advertised by every radio are selectable. The `profile` field selects a catalog ID. Switching an active profile retains the first original-value snapshot, so restoration returns to settings before the first change. Actual native mappings remain firmware-dependent; public status verifies the resulting live country after reboot.

Actions save native selectors once and require a router reboot. Startup and status polling never modify NVRAM or restart Wi-Fi. Public status reports the saved mode, pending reboot or verification state, current country/chanspec, available 20 MHz channels and base/additional exclusion masks. Availability does not establish successful RF operation or regulatory approval.

The original selectors, including absent versus empty values, are retained privately in the module data directory. Restore rejects conflicting external changes instead of overwriting them. Removal restores owned selectors when possible; a reboot is still required. Disable and USB removal leave saved native settings in place. Restore and reboot before removing the module for immediate confirmation of the original live state.
