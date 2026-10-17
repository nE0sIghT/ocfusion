# Architecture

OCFusion runs beside stock ASUSWRT. ASUS owns the radios, Wi-Fi/VLAN topology,
SDN configuration and administrator session. OCFusion adds module supervision
and a separate extension panel; it does not replace firmware or persistently
modify the read-only root filesystem.

## Components and ownership

| Component                        | Responsibility                                                                                                                                                |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `platform/`                      | extd module manager and extctl CLI: registry, verified package snapshots, process groups, lifecycle operations, configuration transactions and UI publication |
| `web/`                           | Shared panel: native header/session, module menu/cards, iframe navigation, browser transport, styles and language engine                                      |
| `modules/vpn/daemon/`            | Native VPN connection registry, Fusion/SDN discovery, TUN, policy routing, NAT, DNS, kill switch and a private VPN API                                        |
| `modules/openconnect/daemon/`    | VPN profiles and credentials, OpenConnect supervision, authentication, DTLS and tunnel events                                                                 |
| `modules/openconnect/packaging/` | Module manifest/lifecycle adapter, tunnel hook and profile example                                                                                            |
| `modules/openconnect/ui/`        | Client settings, status, actions and translations                                                                                                             |
| `modules/radio-settings/`        | Native regional selectors, original-setting recovery, radio diagnostics and wireless-page contribution                                                        |
| `modules/adguard-home/`          | DNS filtering adapter, private upstream control, UI and source patches                                                                                        |
| `modules/vpn/ui/`                | Managed Fusion notice script                                                                                                                                  |
| `packaging/`                     | Router installation, USB preparation/bootstrap and ASUS Apps entrypoints                                                                                      |
| `tools/`, `meson.build`          | Pinned-source builds, runtime staging, archive generation and release upload                                                                                  |
| `tests/`                         | Host integration/browser fixtures; Go package tests remain beside their code                                                                                  |

Modules are trusted administrator executables, not Go plugins. The manager
supervises their process groups and invokes declared operations with a JSON
stdin envelope. Capabilities/resource declarations describe requirements and
ownership; they do not sandbox privileges. See the [module contract](modules.md)
for the package and IPC interfaces.

The platform owns shared web publication, the stock header icon, language/model
bootstrap and the authenticated stock upload/RAM command channel. It runs
without a VPN module or client. The VPN provider exposes API 1 over a private
Unix socket; OpenConnect declares that dependency in its manifest. Generic
manager dispatch contains no OpenConnect-specific operations.

## Storage and startup

| Location                             | Contents                                                                                   |
| ------------------------------------ | ------------------------------------------------------------------------------------------ |
| USB Apps root, exposed as `/tmp/opt` | Installed distribution, web assets, native Apps entrypoints and additional module versions |
| `/jffs/ocfusion-platform`            | extd/extctl, registry and private generic module configuration                             |
| `/jffs/ocfusion`                     | OpenConnect private profile, client CLI/hook and client payload manifest                   |
| `/jffs/ocfusion-vpn`                 | Native VPN connection registry and ownership                                               |
| `/jffs/ocfusion-update-backup`       | Temporary transaction backup of platform and declared module state                         |
| `/tmp/ocfusion-platform`             | Private Unix control socket, verified module snapshots and process state                   |
| `/tmp/ocfusion`                      | OpenConnect RAM executable, materialized credentials/CA, tunnel events and client status   |
| `/tmp/ocfusion-adguard-home`         | Private AdGuard control socket, generated configuration and bounded working data           |
| `/tmp/ocfusion-vpn`                  | Private VPN API socket and network status                                                  |
| `/tmp/var/wwwext`                    | Published common panel, module metadata and UI assets                                      |

ASUS Apps starts `S95ocfusion` from USB. It starts extd and registers a cron
watchdog; extd starts enabled modules. Providers start before their dependents
and stop after them. VPN network guards remain active when a client profile is
disconnected. Client connection settings are distinct from enabling or disabling
a module process.

USB preparation places a temporary Apps entry that runs the installer using the
stock Apps lifecycle. The USB marker is detected by firmware rather than
directly executed. Successful installation removes the temporary entry; failed
installation remains eligible for retry. USB is still needed for package
validation, assets and startup even though active executables are staged in RAM.

## Web and request flow

The panel uses the native ASUS server, login, CSS and language selector. A RAM
bind overlay appends OCFusion JavaScript to the current `/www/state.js`. It adds
one leading OCFusion icon
<img src="../web/icon.png" alt="OCFusion" width="24" height="24"> and loads
declared module contributions on supported stock pages. The script waits for the
header DOM node and tolerates unrelated firmware changes. Mount ownership checks
prevent removing another extension's overlay. The panel also has a direct URL.
Firmware assets are referenced on the running router, not redistributed. Own
panel scripts load in order with fresh URLs on each page load; reload an already
open panel after an update.

The manager publishes each manifest `ui` directory under
`/user/extensions-modules/<id>/...` and generates menu/card metadata. The shared
panel opens these pages in one iframe. Module pages reuse the parent language
and common styles; they do not nest another ASUS header/sidebar. There are no
build-time module HTML/JavaScript insertions.

Routine platform/module status is read from RAM JSON through the stock web
server. Modules atomically write a bounded redacted snapshot to the
manager-provided `EXT_PUBLIC_STATUS_PATH`; the manager publishes a fresh
envelope for running workers. OpenConnect aggregates public status for its
profiles, rejecting stale source data and excluding request replies and raw
error text. Missing, stopped or stale producers yield unavailable data. Status
polling does not use NVRAM.

Commands and settings requests follow this path:

```text
Module page -> parent EXTENSIONS API -> authenticated POST /splash_page.cgi
            -> owned CSS symlink -> private RAM inbox -> completed-write event
            -> CRC32/ID/expiry validation -> extctl -> private Unix socket -> extd
            -> module entrypoint -> CRC32-protected RAM JSON reply -> browser
```

One shared inbox and reply are used. Each request carries a random ID, the
receiver instance, a short expiry and CRC32/IEEE of the exact UTF-8 payload
string; replies carry the same ID/instance and their own payload CRC32. The
manager waits for completed writes, rejects damaged/expired envelopes, clears
consumed input and validates declared actions before invoking the module. CRC32
detects accidental corruption, not forgery; authorization belongs to the stock
server. Each panel permits one command at a time. Concurrent tabs can overwrite
requests or replies and time out; modifying commands are never automatically
retried, and a timeout does not prove that an operation was not executed. Module
revision checks still arbitrate competing configuration changes. Public registry
metadata and UI files contain no private configuration. Modules own their
response redaction; the transport does not make arbitrary diagnostic JSON safe.

OpenConnect's settings action returns public fields and secret-presence flags.
Its update action applies a revision-checked patch to the private VPN profile,
preserving unchanged credentials. Full configuration uses validation/import
through the module adapter. That private profile remains authoritative; the
manager's generic configuration copy is not a replacement for it. Credentials
are materialized privately in RAM for the client.

## VPN and native networks

The platform does not allocate a native VPN profile for web transport. The VPN
provider owns connection records by module/profile and allocates native
bindings, interfaces and routing resources independently. The managed binding's
switch stays OFF. Firmware version changes do not block existing connections.
ASUS owns Wi-Fi/VLAN/SDN topology. The VPN provider discovers assigned bridges
and installs policy routes and forwarding/NAT rules. A tunnel default route is
paired with an unreachable fallback; assigned IPv6 traffic is blocked.
Server-advertised split exclusions are ignored for the managed IPv4 full tunnel.

The managed OpenConnect patch keeps its hook environment bounded when a server
advertises a large route list. OpenConnect normalizes hook events into
interface/address/MTU parameters and sends them to the VPN provider. The client
reports UP only after the provider acknowledges application. Each connection has
independent runtime state and rules. The provider independently expires client
heartbeats, detects loss of the tunnel interface and reconciles network rules.
Client authentication credentials and server trust are separate: public roots
plus an optional additional CA validate the server, while the client
certificate/key authenticate the client.

DNS servers for each Wi-Fi/VLAN network remain configured in ASUS. The provider
does not redirect queries addressed to external DNS servers or apply
tunnel-advertised DNS. Requests to external DNS addresses follow the network's
normal VPN routes and kill switch; requests to the router use its DNS service
and the global DNS policy below.

## Global DNS and filtering

The VPN provider owns a persistent global DNS preference keyed by
module/profile. It discovers the stock dnsmasq/stubby upstream endpoints and
reserves a free mark bit, rule priority and route table. Router OUTPUT DNS is
routed through the selected live tunnel and NATed there. The native resolver
configuration stays authoritative. Loss of the connection removes steering and
restores ordinary WAN DNS. IPv6 native upstream configurations explicitly remain
on WAN in this IPv4 implementation. Existing per-network VPN forwarding guards
are independent of this global DNS policy.

The independent AdGuard Home module registers a short resolver lease with the
provider without allocating a Fusion profile. The provider captures IPv4 queries
addressed to router DNS, preserving client source IP. AdGuard listens on a
separate DNS port and uses marked backend sockets to avoid recursive capture. By
default, per-network client mappings send allowed requests to that network's
native resolver. Explicit custom DNS/DoT/DoH upstreams use the same mark and
recreate connections on a VPN/WAN transition.

The module ships a small curated catalog of DNS-compatible filter subscriptions.
A new installation selects AdGuard DNS filter while leaving protection disabled.
Stable URL-derived list IDs preserve cached content when selection order
changes. AdGuard downloads lists and updates them every 24 hours; failed
downloads retain the prior rules and expose a failure flag without advancing the
successful-update timestamp. The list cache stays in RAM and is downloaded again
after reboot. Manual updates use an asynchronous private request so downloads do
not block the platform command channel. The upstream administrator API remains
private.

AdGuard is source-built with tracked patches for a headless entrypoint, private
Unix administrator HTTP control and marked DNS dialers. Its native public
administrator UI, DHCP and TCP administrator listeners are disabled. The normal
authenticated module UI is used. Configuration is platform-owned private module
data; caches, query logs and statistics are bounded RAM data. Filter capture is
removed on disable, lease expiry or provider recovery. No OpenConnect dependency
is required.

Manual OpenConnect OTP uses a structured pipe from the patched authentication
callback and a private per-worker Unix socket. The worker publishes only a
random challenge ID and expiry. Responses are bounded, generation-bound and
one-use. Automatic TOTP uses native OpenConnect token generation from a private
RAM secret file and requires synchronized router time.

## Updates and removal

The binary archive keeps `install.sh` as its only executable at the top level.
USB preparation helpers are in `scripts/`, ASUS Apps entries are in `init/`, and
the manager is in `bin/`. Bundled modules are complete packages under
`modules/<id>/`, each with its own manifest, executables, UI and resources. The
installer registers every bundled package through the manager. OpenConnect owns
its JFFS data and installed tunnel hook. `THIRD-PARTY.md` contains the complete
dependency license notices; build inputs remain in the source archive.

The installer verifies payload checksums and validates the bundled package set.
It runs opted-in preflight hooks before changes, backs up platform and
manifest-declared module state together with native VPN records and custom
fields, then swaps owned files. Installation hooks run in dependency order
before the bundled package set is registered atomically. Updates preserve
private profiles, the Fusion index and SDN bindings. Empty bundled sets are
supported; independently installed modules remain registered. Interrupted
updates use owned backups for recovery; successful updates remove temporary
backups. Service restart briefly interrupts VPN traffic.

Updates retain the current connection registry and private OpenConnect profiles.
Additional profiles use separate persistent and RAM directories. The normal
installation transaction backs up declared state and restores it on failure,
including updates from a replacement USB drive with no old payload.

The uploader uses an owned, unassigned CSS resource linked to the private RAM
inbox. It does not modify captive portal configuration. Owned links are removed
on shutdown, consumed input is cleared, and replies never echo private request
payloads.

The public module snapshot includes platform readiness and a periodically
refreshed timestamp. The installer marks installation in progress before
stopping the previous manager; this state overrides readiness until the
transaction finishes. A clean manager shutdown publishes STOPPED and stopped
module states before exiting. The panel hides module frames and rejects actions
while installation is pending, services are stopped or the snapshot is stale. It
uses the ASUS HTTP Date header for timestamp comparisons where available. The
published files can remain in RAM after USB removal; their presence alone does
not indicate a running service.

Safe USB removal stops the Apps-managed services. Removing storage and rebooting
disables startup but retains JFFS settings and native bindings. Successful
module removal can report `reboot_required`; the manager retains a private RAM
notice until reboot so a partial removal or retry cannot lose it. The installer
reports the notice even when saved data is pruned. Failed cleanup retains the
module registration and blocks further platform deletion.

Full uninstall first requires reassignment of dependent networks, then removal
of owned runtime/registry objects. See
[installation and operation](../INSTALLATION.md) for the procedures.

## Native radio settings

The independent `radio-settings` module requests a native ASUS regional preset
through `location_code`, `ui_location_code` and per-radio advertised-country
overrides. ASUS remains responsible for driver initialization, board
country/revision, base channel-group masks and channel selection. The module
does not patch firmware, load a replacement CLM, edit factory territory/OTP or
intercept native startup.

An explicit action saves a private original-value snapshot before changing the
owned selectors and committing NVRAM once. A per-module file lock and revision
checks serialize modifying requests. Interrupted writes retain the snapshot for
restoration. Applying and restoring require a reboot because base group masks
are read during driver attachment; a normal Wi-Fi service restart is not assumed
to reload them. Read-only startup/polling compares the boot identity, saved
selectors and live radio country. A saved profile is not reported active before
a reboot and matching live state.

The module's stock wireless-page contribution removes the additional factory-SKU
cutoff from the 6 GHz PSC subset. It uses the stock channel set, leaving
hardware, channel-width and Mesh constraints intact. It changes only the page
model and selector, not NVRAM, factory territory or native automatic channel
selection.

The regional catalog maps native ASUS selectors to country/revision pairs.
Driver revision support determines availability; matching live state after
reboot confirms application. Profile changes retain the original snapshot and
reject externally changed selectors. The profiles change regulatory power
constraints as well as channels. The module publishes actual driver channels and
masks rather than promising that a regional selection unlocks specific
frequencies on every radio. Independently changed native selectors cause
restoration to reject the conflict. Saved settings survive module disable or
loss of USB; removal restores selectors but the running radios retain their
prior state until reboot.
