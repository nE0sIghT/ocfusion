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
    '@OUTPUT@',
    '--c-flags',
    c_flags,
    '--jobs',
    get_option('external_jobs').to_string(),
]

# Ordered dependencies stay in Meson; upstream build recipes live in build.json.
components = [
    {'name': 'musl', 'archive': 'musl-1.2.6.tar.gz', 'dependencies': []},
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
