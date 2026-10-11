vpn_go = files(
    'daemon/go.mod',
    'daemon/config.go',
    'daemon/broker.go',
    'daemon/main.go',
    'daemon/registry.go',
    'daemon/network.go',
    'daemon/router.go',
    'daemon/service.go',
)

vpn_service = custom_target(
    'ocfusion-vpn',
    input: vpn_go,
    output: 'ocfusion-vpn',
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go',
        go.full_path(),
        '--module',
        source_root / 'modules/vpn/daemon',
        '--output',
        '@OUTPUT@',
        '--version',
        version,
        '--revision',
        revision,
    ],
)
