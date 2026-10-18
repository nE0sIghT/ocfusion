runtime_go = files(
    'daemon/go.mod',
    'daemon/client_output.go',
    'daemon/config.go',
    'daemon/otp.go',
    'daemon/daemon.go',
    'daemon/install.go',
    'daemon/main.go',
    'daemon/profiles.go',
    'daemon/profile_settings.go',
    'daemon/router.go',
    'daemon/status.go',
    'daemon/public_status.go',
    'daemon/vpn.go',
)

supervisor = custom_target(
    'ocfusion-openconnect',
    input: runtime_go,
    output: 'ocfusion-openconnect',
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go',
        go.full_path(),
        '--module',
        source_root / 'modules/openconnect/daemon',
        '--output',
        '@OUTPUT@',
        '--version',
        version,
        '--revision',
        revision,
    ],
)

module_specs += {
    'openconnect': {
        'binaries': [openconnect, supervisor],
        'assets': files(
            'packaging/module.json.in',
            'packaging/scripts/module.sh',
            'packaging/scripts/vpnc-script',
            'packaging/examples/profile.occonf',
            'ui/settings.js',
            'ui/index.html',
            'ui/app.js',
            'ui/layout.js',
            'ui/layout.css',
            'ui/connection.js',
            'ui/translations.js',
        ),
        'notices': [
            build_root / 'licenses/OpenConnect-LGPL-2.1.txt',
            build_root / 'licenses/OpenSSL-Apache-2.0.txt',
            build_root / 'licenses/libxml2-Copyright.txt',
            build_root / 'licenses/musl-COPYRIGHT.txt',
            build_root / 'licenses/zlib-README.txt',
        ],
        'data': [{'source': downloads['cacert-2026-09-25.pem'], 'name': 'ca-bundle.crt'}],
    },
}
