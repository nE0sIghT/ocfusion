# Host tests never execute ARM64 binaries and do not require a cross compiler.
foreach module : [
    'modules/openconnect/daemon',
    'modules/vpn/daemon',
    'modules/adguard-home/daemon',
    'modules/radio-settings/daemon',
    'platform',
]
    test(
        module + '-race',
        go,
        args: ['test', '-race', './...'],
        workdir: source_root / module,
        suite: 'unit',
        timeout: 120,
    )

    test(
        module + '-vet',
        go,
        args: ['vet', './...'],
        workdir: source_root / module,
        suite: 'unit',
        timeout: 120,
    )
endforeach

foreach script : [
    'tests/test-installer.py',
    'tests/test-usb.py',
    'tests/test-version.py',
    'tests/test-release.py',
    'tools/check-source.py',
    'tools/check-go-format.py',
]
    test(
        script,
        python,
        args: [files(source_root / script)],
        suite: 'unit',
        timeout: 120,
    )
endforeach

test(
    'web-mailbox',
    node,
    args: [files('test-web.js')],
    suite: 'unit',
)

test(
    'module-example',
    python,
    args: [files('test-example.py')],
    suite: 'example',
    timeout: 120,
)

if get_option('browser_tests')
    test(
        'browser-radio-settings',
        node,
        args: [files('test-radio-ui.cjs')],
        suite: 'browser',
        timeout: 120,
    )

    test(
        'browser-i18n',
        node,
        args: [files('test-i18n.cjs')],
        suite: 'browser',
        timeout: 120,
    )
endif

# Named test targets reuse this directory's configuration and native test runner.
meson_command = find_program('meson')
foreach suite : ['unit', 'example']
    run_target(
        'test-' + suite,
        command: [
            meson_command,
            'test',
            '-C',
            build_root,
            '--no-rebuild',
            '--suite',
            suite,
            '--print-errorlogs',
        ],
    )
endforeach

if get_option('browser_tests')
    run_target(
        'test-browser',
        command: [
            meson_command,
            'test',
            '-C',
            build_root,
            '--no-rebuild',
            '--suite',
            'browser',
            '--print-errorlogs',
        ],
    )
endif

if get_option('runtime')
    test(
        'managed-hook',
        python,
        args: [
            files('test-managed-env.py'),
            '--source',
            build_root / 'src/openconnect-9.21/script.c',
        ],
        depends: openconnect,
        suite: 'client',
        timeout: 120,
    )
endif

if get_option('runtime')
    test(
        'managed-otp',
        python,
        args: [
            files('test-otp-client.py'),
            '--source', build_root / 'src/openconnect-9.21/main.c',
        ],
        depends: openconnect,
        suite: 'client',
        timeout: 30,
    )

    adguard_fixture = custom_target(
        'adguard-home-native',
        input: downloads['AdGuardHome-v0.107.79.tar.gz'],
        output: 'AdGuardHome-native',
        depend_files: [
            adguard_builder,
            files('../modules/adguard-home/patches/0001-private-headless-control.patch'),
            files('../modules/adguard-home/patches/0002-dnsproxy-marked-dialers.patch'),
            files('../modules/adguard-home/patches/0003-filter-update-status.patch'),
        ],
        command: [
            python, adguard_builder,
            '--archive', '@INPUT@',
            '--go', go.full_path(),
            '--output', '@OUTPUT@',
            '--build-root', build_root / 'adguard-fixture',
            '--architecture', 'amd64',
        ],
    )

    test(
        'adguard-home-dns',
        go,
        args: ['test', '-race', '-run', 'TestSourceBuiltAdGuard', './...'],
        workdir: source_root / 'modules/adguard-home/daemon',
        env: {'OCF_ADGUARD_BINARY': adguard_fixture.full_path()},
        depends: adguard_fixture,
        suite: 'client',
        timeout: 120,
    )
endif
