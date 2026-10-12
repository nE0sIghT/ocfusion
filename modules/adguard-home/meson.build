adguard_adapter = custom_target(
    'ocfusion-adguard-home',
    input: files(
        'daemon/go.mod',
        'daemon/config.go',
        'daemon/filters.go',
        'daemon/catalog.json',
        'daemon/provider.go',
        'daemon/main.go',
        'daemon/log.go',
    ),
    output: 'ocfusion-adguard-home',
    depend_files: go_builder,
    command: [
        python,
        go_builder,
        '--go', go.full_path(),
        '--module', source_root / 'modules/adguard-home/daemon',
        '--output', '@OUTPUT@',
        '--version', version,
        '--revision', revision,
    ],
)

adguard_builder = files('../../tools/build-adguard.py')
adguard_home = custom_target(
    'AdGuardHome',
    input: downloads['AdGuardHome-v0.107.79.tar.gz'],
    output: 'AdGuardHome',
    depend_files: [
        adguard_builder,
        files('patches/0001-private-headless-control.patch'),
        files('patches/0002-dnsproxy-marked-dialers.patch'),
        files('patches/0003-filter-update-status.patch'),
    ],
    command: [
        python,
        adguard_builder,
        '--archive', '@INPUT@',
        '--go', go.full_path(),
        '--output', '@OUTPUT@',
        '--build-root', build_root,
    ],
)
