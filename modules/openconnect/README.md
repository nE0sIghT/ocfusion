# OpenConnect module

This module owns OpenConnect profiles, certificate authentication, CA handling,
DTLS, client supervision and tunnel-hook events. Its manifest requires the VPN
provider's API 1.

- `daemon/` builds `ocfusion-openconnect`, the client supervisor and settings
  API.
- `packaging/` contains the ordinary module manifest, lifecycle adapter, tunnel
  hook and profile example.
- `patches/` contains the tracked patches applied to the pinned upstream client.
- `ui/` contains the module page, settings, status and EN/RU translations.

The VPN provider owns native Fusion/SDN, routing, firewall and DNS. The platform
owns the shared web panel and browser transport. OpenConnect passes normalized
tunnel parameters through the private VPN API; credentials stay inside this
module.

Private profiles retain `/jffs/ocfusion` during upgrades.
`/jffs/ocfusion/ocfusion` is the installed CLI path; the package executable is
`bin/ocfusion-openconnect`. The module uses the same manifest, lifecycle, iframe
and browser API as externally installed packages. See the
[module contract](../../docs/modules.md).

The default profile retains its existing private location. Additional profile
IDs use `profiles/<id>/` below the module state/RAM directories and receive
independent provider connections. The foreground supervisor manages profile
workers; each worker owns its OpenConnect child and credential files. Client
settings actions identify the target profile explicitly.
