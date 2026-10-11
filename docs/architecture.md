# Architecture

OCFusion runs beside stock ASUSWRT. ASUS owns the radios, Wi-Fi/VLAN topology, SDN configuration and administrator session. OCFusion adds module supervision and a separate extension panel; it does not replace firmware or persistently modify the read-only root filesystem.

## Components and ownership

| Component | Responsibility |
| --- | --- |
| `platform/` | extd module manager and extctl CLI: registry, verified package snapshots, process groups, lifecycle operations, configuration transactions and UI publication |
| `web/` | Shared panel: native header/session, module menu/cards, iframe navigation, browser transport, styles and language engine |
| `modules/vpn/daemon/` | Native VPN connection registry, Fusion/SDN discovery, TUN, policy routing, NAT, DNS, kill switch and a private VPN API |
| `modules/openconnect/daemon/` | VPN profiles and credentials, OpenConnect supervision, authentication, DTLS and tunnel events |
| `modules/openconnect/packaging/` | Module manifest/lifecycle adapter, tunnel hook and profile example |
| `modules/openconnect/ui/` | Client settings, status, actions and translations |
| `modules/vpn/ui/` | Managed Fusion notice script |
| `packaging/` | Router installation, USB preparation/bootstrap and ASUS Apps entrypoints |
| `tools/`, `meson.build` | Pinned-source builds, runtime staging, archive generation and release upload |
| `tests/` | Host integration/browser fixtures; Go package tests remain beside their code |

Modules are trusted administrator executables, not Go plugins. The manager supervises their process groups and invokes declared operations with a JSON stdin envelope. Capabilities/resource declarations describe requirements and ownership; they do not sandbox privileges. See the [module contract](modules.md) for the package and IPC interfaces.

The platform owns shared web publication, the stock header icon, language/model bootstrap and the authenticated NVRAM mailbox. It runs without a VPN module or client. The VPN provider exposes API 1 over a private Unix socket; OpenConnect declares that dependency in its manifest. Generic manager dispatch contains no OpenConnect-specific operations.

## Storage and startup

| Location | Contents |
| --- | --- |
| USB Apps root, exposed as `/tmp/opt` | Installed distribution, web assets, native Apps entrypoints and additional module versions |
| `/jffs/ocfusion-platform` | extd/extctl, registry and private generic module configuration |
| `/jffs/ocfusion` | OpenConnect private profile, compatibility CLI/hook and client payload manifest |
| `/jffs/ocfusion-vpn` | Native VPN connection registry and ownership |
| `/jffs/ocfusion-update-backup` | Temporary transaction backup of platform and declared module state |
| `/tmp/ocfusion-platform` | Private Unix control socket, verified module snapshots and process state |
| `/tmp/ocfusion` | OpenConnect RAM executable, materialized credentials/CA, tunnel events and client status |
| `/tmp/ocfusion-vpn` | Private VPN API socket and network status |
| `/tmp/var/wwwext` | Published common panel, module metadata and UI assets |

ASUS Apps starts `S95ocfusion` from USB. It starts extd and registers a cron watchdog; extd starts enabled modules. Providers start before their dependents and stop after them. VPN network guards remain active when a client profile is disconnected. Client connection settings are distinct from enabling or disabling a module process.

USB preparation places a temporary Apps entry that runs the installer using the stock Apps lifecycle. The USB marker is detected by firmware rather than directly executed. Successful installation removes the temporary entry; failed installation remains eligible for retry. USB is still needed for package validation, assets and startup even though active executables are staged in RAM.

## Web and request flow

The panel uses the native ASUS server, login, CSS and language selector. A RAM bind overlay of the verified original `/www/state.js` adds one leading OCFusion icon <img src="../web/icon.png" alt="OCFusion" width="24" height="24"> and loads declared module contributions on supported stock pages. Overlay ownership/hash checks prevent replacing an unknown page. The panel also has a direct URL. Firmware assets are referenced on the running router, not redistributed.

The manager publishes each manifest `ui` directory under `/user/extensions-modules/<id>/...` and generates menu/card metadata. The shared panel opens these pages in one iframe. Module pages reuse the parent language and common styles; they do not nest another ASUS header/sidebar. There are no build-time module HTML/JavaScript insertions.

Routine platform/module status is read from RAM JSON through the stock web server. Modules atomically write a bounded redacted snapshot to the manager-provided `EXT_PUBLIC_STATUS_PATH`; the manager publishes a fresh envelope for running workers. OpenConnect aggregates public status for its profiles, rejecting stale source data and excluding request replies and raw error text. Missing, stopped or stale producers yield unavailable data. Status polling does not use NVRAM.

Commands and settings requests follow this path:

```text
Module page -> parent EXTENSIONS API -> authenticated ASUS NVRAM mailbox
            -> platform mailbox handler -> extctl -> private Unix socket -> extd
            -> module entrypoint -> JSON response -> nonce-matched browser reply
```

The mailbox serializes requests and bounds payloads. Replies use volatile NVRAM writes without an explicit commit; module settings retain their own persistence. This does not establish whether the stock HTTP request writer commits internally. Configuration/action uploads use chunks, size/hash validation and final commit. The manager validates declared actions before invoking the module. Public registry metadata and UI files contain no private configuration. Modules own their response redaction; the transport does not make arbitrary diagnostic JSON safe.

OpenConnect's settings action returns public fields and secret-presence flags. Its update action applies a revision-checked patch to the private VPN profile, preserving unchanged credentials. Full configuration uses validation/import through the module adapter. That private profile remains authoritative; the manager's generic configuration copy is not a replacement for it. Credentials are materialized privately in RAM for the client.

## VPN and native networks

The stock HTTP writer accepts the reserved OpenVPN custom field used by the mailbox. The platform owns that reservation independently of VPN clients. The VPN provider manages connection records by module/profile owner and allocates separate native bindings, interfaces and routing resources. Its first connection can lease the platform reservation without owning its lifetime; its switch stays OFF. Removing a client does not remove the platform transport. ASUS owns Wi-Fi/VLAN/SDN topology. The VPN provider discovers assigned bridges and installs policy routes and forwarding/NAT/DNS rules. A tunnel default route is paired with an unreachable fallback; assigned IPv6 traffic is blocked. Server-advertised split exclusions are ignored for the managed IPv4 full tunnel.

The managed OpenConnect patch keeps its hook environment bounded when a server advertises a large route list. OpenConnect normalizes hook events into interface/address/MTU/DNS parameters and sends them to the VPN provider. The client reports UP only after the provider acknowledges application. Each connection has independent runtime state and rules. The provider independently expires client heartbeats, detects loss of the tunnel interface and reconciles network rules. Client authentication credentials and server trust are separate: public roots plus an optional additional CA validate the server, while the client certificate/key authenticate the client.

## Updates and removal

The binary archive keeps `install.sh` as its only executable at the top level. USB preparation helpers are in `scripts/`, ASUS Apps entries are in `init/`, and the manager is in `bin/`. Bundled modules are complete packages under `modules/<id>/`, each with its own manifest, executables, UI and resources. The installer registers every bundled package through the manager. OpenConnect keeps its historical JFFS data and installed hook locations for upgrades. `THIRD-PARTY.md` contains the complete dependency license notices; build inputs remain in the source archive.

The installer verifies payload checksums and validates the bundled package set. It runs opted-in preflight hooks before changes, backs up platform and manifest-declared module state together with the native transport reservation, then swaps owned files. Installation hooks run in dependency order before the bundled package set is registered atomically. Updates preserve private profiles, the Fusion index and SDN bindings. Empty bundled sets are supported; independently installed modules remain registered. Interrupted updates use owned backups for recovery; successful updates remove temporary backups. Service restart briefly interrupts VPN traffic.

Updates from modular 0.2.9 migrate its single native connection into the connection registry, preserving its Fusion unit/index, interface, route table, priority and SDN assignments. The default OpenConnect profile remains in its current directory, with unchanged credentials. Additional profiles use separate persistent and RAM directories. Pre-0.2.9 MVP installation formats are not supported. The normal installation transaction backs up all declared state before migration and restores it on failure, including updates from a replacement USB drive with no old payload.

The System page contains an experimental upload-channel diagnostic. It sends only synthetic payloads of 1, 8, 32 and 64 KiB through the authenticated stock `splash_page.cgi` uploader. An owned, unassigned CSS resource points to a private RAM inbox; the manager watches completed writes and publishes a nonce-matched synthetic echo through the stock web server. No module commands or configuration changes are accepted through this probe. Owned links are removed on shutdown, and consumed input is cleared. The active command transport remains the NVRAM mailbox until this separate candidate is verified on the router.

The public module snapshot includes platform readiness and a periodically refreshed timestamp. The installer marks installation in progress before stopping the previous manager; this state overrides readiness until the transaction finishes. A clean manager shutdown publishes STOPPED and stopped module states before exiting. The panel hides module frames and rejects actions while installation is pending, services are stopped or the snapshot is stale. It uses the ASUS HTTP Date header for timestamp comparisons where available. The published files can remain in RAM after USB removal; their presence alone does not indicate a running service.

Safe USB removal stops the Apps-managed services. Removing storage and rebooting disables startup but retains JFFS settings and native bindings. Full uninstall first requires reassignment of dependent networks, then removal of owned runtime/registry objects. See [installation and operation](../INSTALLATION.md) for the procedures.
