proxy_adapter = custom_target(
    'ocfusion-http-proxy',
    input: files(
        'daemon/go.mod',
        'daemon/config.go',
        'daemon/provider.go',
        'daemon/main.go',
    ),
    output: 'ocfusion-http-proxy',
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go', go.full_path(),
        '--module', source_root / 'modules/http-proxy/daemon',
        '--output', '@OUTPUT@',
        '--version', version,
        '--revision', revision,
    ],
)

module_specs += {
    'http-proxy': {
        'binaries': [external_targets['3proxy'], proxy_adapter],
        'assets': files(
            'packaging/module.json.in',
            'ui/index.html',
            'ui/app.js',
            'ui/translations.js',
        ),
        'notices': [
            build_root / 'licenses/3proxy-license.txt',
            build_root / 'licenses/musl-COPYRIGHT.txt',
        ],
    },
}
