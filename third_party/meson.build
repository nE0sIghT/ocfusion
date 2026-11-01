fetch = files('../tools/fetch-sources.py')
external_builder = files('../tools/build-external.py')
external_config = files('build.json')
lock = files('sources.lock.json')

# Each immutable input has its own download target.
downloads = {}
foreach name : [
    'musl-1.2.6.tar.gz',
    'zlib-1.3.2.tar.gz',
    'openssl-3.5.9.tar.gz',
    'libxml2-2.15.4.tar.xz',
    'openconnect-9.21.tar.gz',
    'cacert-2026-09-25.pem',
    'AdGuardHome-v0.107.79.tar.gz',
    '3proxy-0.9.9.1.tar.gz',
    'libmnl-1.0.5.tar.bz2',
    'libnfnetlink-1.0.2.tar.bz2',
    'libnetfilter_queue-1.0.5.tar.bz2',
    'lua-5.4.8.tar.gz',
    'zapret2-1.0.6.tar.gz',
    'libcap-2.75.tar.xz',
    'glibc-2.41-queue.h',
]
    downloads += {
        name: custom_target(
            'fetch-' + name,
            output: name,
            input: lock,
            depend_files: fetch,
            command: [python, fetch, '--source', name, '--output', '@OUTPUT@'],
        ),
    }
endforeach

external_command = [
    python,
    external_builder,
    '--archive',
    '@INPUT@',
    '--build-root',
    build_root,
    '--output',
    '@OUTPUT0@',
    '--c-flags',
    c_flags,
    '--jobs',
    get_option('external_jobs').to_string(),
]

# Ordered dependencies stay in Meson; upstream build recipes live in build.json.
components = [
    {'name': 'musl', 'archive': 'musl-1.2.6.tar.gz', 'dependencies': []},
    {
        'name': '3proxy',
        'archive': '3proxy-0.9.9.1.tar.gz',
        'dependencies': ['musl'],
    },
    {'name': 'zlib', 'archive': 'zlib-1.3.2.tar.gz', 'dependencies': ['musl']},
    {
        'name': 'openssl',
        'archive': 'openssl-3.5.9.tar.gz',
        'dependencies': ['musl'],
    },
    {
        'name': 'libxml2',
        'archive': 'libxml2-2.15.4.tar.xz',
        'dependencies': ['musl', 'zlib'],
    },
    {
        'name': 'openconnect',
        'archive': 'openconnect-9.21.tar.gz',
        'dependencies': ['musl', 'zlib', 'openssl', 'libxml2'],
    },
    {
        'name': 'libmnl',
        'archive': 'libmnl-1.0.5.tar.bz2',
        'dependencies': ['musl'],
    },
    {
        'name': 'libnfnetlink',
        'archive': 'libnfnetlink-1.0.2.tar.bz2',
        'dependencies': ['musl'],
    },
    {
        'name': 'libnetfilter_queue',
        'archive': 'libnetfilter_queue-1.0.5.tar.bz2',
        'dependencies': ['musl', 'libmnl', 'libnfnetlink'],
    },
    {'name': 'lua', 'archive': 'lua-5.4.8.tar.gz', 'dependencies': ['musl']},
    {
        'name': 'zapret2',
        'archive': 'zapret2-1.0.6.tar.gz',
        'dependencies': ['musl', 'zlib', 'lua', 'libnetfilter_queue'],
    },
]

external_targets = {}
foreach component : components
    name = component['name']
    dependencies = []
    foreach dependency : component['dependencies']
        dependencies += external_targets[dependency]
    endforeach

    inputs = [external_builder, external_config]
    output = name + '.stamp'
    if name == 'zapret2'
        inputs += files(
            '../modules/zapret2/patches/0001-pass-unmatched-tls-before-reassembly.patch',
            '../modules/zapret2/patches/0002-retry-transient-replay-errors.patch',
            '../modules/zapret2/patches/0003-observe-quic-without-replaying-originals.patch',
        )
        compatibility_headers = custom_target(
            'zapret-compatibility-headers',
            input: [
                downloads['glibc-2.41-queue.h'],
                downloads['libcap-2.75.tar.xz'],
            ],
            output: 'zapret-headers.stamp',
            depends: external_targets['musl'],
            depend_files: files('../tools/prepare-zapret-headers.py'),
            command: [
                python,
                files('../tools/prepare-zapret-headers.py'),
                '--queue',
                '@INPUT0@',
                '--capability',
                '@INPUT1@',
                '--build-root',
                build_root,
                '--output',
                '@OUTPUT@',
            ],
        )
        dependencies += compatibility_headers
        output = ['nfqws2', 'zapret-lib.lua', 'zapret-antidpi.lua',
            'dtls_clienthello_w3_org.bin', 'wireguard_initiation.bin',
            'stun.bin', 'discord-ip-discovery-with-port.bin']
    endif

    if name == '3proxy'
        output = '3proxy'
    endif

    if name == 'openconnect'
        inputs += files(
            '../modules/openconnect/patches/0001-managed-hook-environment.patch',
            '../modules/openconnect/patches/0002-managed-otp-channel.patch',
        )

        output = 'openconnect'
    endif

    external_targets += {
        name: custom_target(
            name,
            input: downloads[component['archive']],
            output: output,
            depends: dependencies,
            depend_files: inputs,
            command: external_command + ['--component', name],
        ),
    }
endforeach

openconnect = external_targets['openconnect']
