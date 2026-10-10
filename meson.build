project(
    'ocfusion',
    version: run_command('python3', 'tools/version.py', check: true).stdout().strip(),
    meson_version: '>=1.12.1',
    license: 'GPL-3.0-or-later',
)

python = find_program('python3')
go = find_program('go')
version = run_command(
    python,
    files('tools/version.py'),
    '--override',
    get_option('release_version'),
    check: true,
).stdout().strip()
message('Resolved build version: ' + version)
node = find_program('node')
source_root = meson.project_source_root()
build_root = meson.project_build_root()

if get_option('runtime')
    if not meson.is_cross_build() or host_machine.cpu_family() != 'aarch64'
        error(
            'Use --cross-file build-env/armv8-a-crypto-crc.ini for router builds, or -Druntime=false for host tests.',
        )
    endif

    go_version = run_command(go, 'env', 'GOVERSION', check: true).stdout().strip()
    if go_version != 'go1.27.2'
        error('Go 1.27.2 required; use the pinned build environment.')
    endif

    cross_gcc = find_program('aarch64-linux-gnu-gcc')
    if run_command(cross_gcc, '-dumpfullversion', check: true).stdout().strip() != '14.2.0'
        error('Cross GCC 14.2.0 required; use the pinned build environment.')
    endif

    c_flags = meson.get_external_property('c_flags')
    go_builder = files('tools/build-go.py')

    subdir('third_party')
    subdir('platform')
    subdir('modules/vpn')
    subdir('modules/openconnect')
    subdir('packaging')
endif

subdir('tests')
