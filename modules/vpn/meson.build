vpn_go = files(
    'daemon/go.mod',
    'daemon/go.sum',
    'daemon/destination.go',
    'daemon/destination_route.go',
    'daemon/destination_firewall.go',
    'daemon/destination_dns.go',
    'daemon/config.go',
    'daemon/backup.go',
    'daemon/dns.go',
    'daemon/resolver.go',
    'daemon/broker.go',
    'daemon/egress.go',
    'daemon/main.go',
    'daemon/registry.go',
    'daemon/network.go',
    'daemon/router.go',
    'daemon/service.go',
)

vpn_service = custom_target(
    'ocfusion-vpn',
    input: vpn_go,
    output: ['ocfusion-vpn', 'vpn-Go-dependencies.txt'],
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go',
        go.full_path(),
        '--module',
        source_root / 'modules/vpn/daemon',
        '--output',
        '@OUTPUT0@',
        '--notices',
        '@OUTPUT1@',
        '--version',
        version,
        '--revision',
        revision,
    ],
)

module_specs += {
    'vpn': {
        'binaries': [vpn_service[0]],
        'assets': files(
            'packaging/module.json.in',
            'ui/fusion.js',
        ),
        'notices': [vpn_service[1].full_path()],
    },
}
