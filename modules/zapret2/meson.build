zapret_adapter = custom_target(
    'ocfusion-zapret2',
    input: files(
        'daemon/go.mod',
        'daemon/config.go',
        'daemon/strategy.go',
        'daemon/presets.go',
        'daemon/scan.go',
        'daemon/scan_worker.go',
        'daemon/firewall.go',
        'daemon/main.go',
        'daemon/log.go',
    ),
    output: 'ocfusion-zapret2',
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go',
        go.full_path(),
        '--module',
        source_root / 'modules/zapret2/daemon',
        '--output',
        '@OUTPUT@',
        '--version',
        version,
        '--revision',
        revision,
    ],
)

module_specs += {
    'zapret2': {
        'binaries': [external_targets['zapret2'][0], zapret_adapter],
        'assets': files(
            'packaging/module.json.in',
            'ui/index.html',
            'ui/app.js',
            'ui/layout.js',
            'ui/targets.js',
            'ui/layout.css',
            'ui/translations.js',
        ),
        'generated_data': [
            {
                'source': external_targets['zapret2'][1],
                'name': 'lua/zapret-lib.lua',
            },
            {
                'source': external_targets['zapret2'][2],
                'name': 'lua/zapret-antidpi.lua',
            },
        ],
        'notices': [
            build_root / 'licenses/zapret2-license.txt',
            build_root / 'licenses/zapret2-compatibility-headers.txt',
            build_root / 'licenses/libmnl-COPYING.txt',
            build_root / 'licenses/libnfnetlink-COPYING.txt',
            build_root / 'licenses/libnetfilter_queue-COPYING.txt',
            build_root / 'licenses/Lua-license.html',
            build_root / 'licenses/zlib-README.txt',
            build_root / 'licenses/musl-COPYRIGHT.txt',
        ],
    },
}
