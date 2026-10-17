# VPN provider

The VPN provider owns native Fusion profiles, SDN discovery, TUN preparation,
kill switch, routing, forwarding, NAT and DNS. It allocates independent
resources for each module/profile owner through API 1, without receiving client
credentials.

Disconnecting or losing a client retains its network guards. Removal requires
reassignment of its native networks. The platform web transport is independent
of Fusion profiles; removing a connection does not remove web transport.

`daemon/` builds `ocfusion-vpn`, `packaging/` contains its manifest and `ui/`
contains the stock Fusion contribution. This API-only module has no settings
page. See the
[VPN API and module contract](../../docs/modules.md#vpn-provider-api-1).
