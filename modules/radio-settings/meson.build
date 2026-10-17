radio_service = custom_target(
    'ocfusion-radio-settings',
    input: files(
        'daemon/go.mod',
        'daemon/main.go',
        'daemon/settings.go',
        'daemon/profiles.go',
        'daemon/hardware.go',
    ),
    output: 'ocfusion-radio-settings',
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go', go.full_path(),
        '--module', source_root / 'modules/radio-settings/daemon',
        '--output', '@OUTPUT@',
        '--version', version,
        '--revision', revision,
    ],
)
