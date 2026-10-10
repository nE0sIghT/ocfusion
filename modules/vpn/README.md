# VPN provider

The VPN provider owns native connection bindings, SDN discovery, TUN preparation, kill switch, routing, forwarding, NAT and DNS. It provides a private versioned API to VPN client modules, independently of their authentication and protocol implementation.

The current implementation manages one connection. Its native Fusion index is leased from the platform's stock mailbox reservation. Migration preserves the existing index and SDN assignments. Disconnecting or losing a client retains the network guards; removing a connection requires reassignment of its native networks first.

`daemon/` builds `ocfusion-vpn`. `packaging/` contains its manifest. `ui/` contains the integration page and the declared stock Fusion contribution. See the [VPN API and module contract](../../docs/modules.md#vpn-provider-api-1).
