runtime_assets = files(
    '../LICENSE',
    '../README.md',
    '../INSTALLATION.md',
    'install.sh',
    'scripts/prepare-usb.sh',
    'init/S94ocfusionprep',
    'init/S95ocfusion',
    '../web/import.html',
    '../web/panel.js',
    '../web/panel.css',
    '../web/i18n.js',
    '../web/logo.png',
    '../web/icon.png',
)
common_notices = files(
    '../third_party/notices/GCC-runtime-copyright.txt',
    '../third_party/notices/Go-BSD.txt',
    '../third_party/notices/Mozilla-MPL-2.0.txt',
)

selected_modules = get_option('modules')
if selected_modules == ['all']
    selected_modules = module_specs.keys()
endif
foreach module_id : selected_modules
    if not module_specs.has_key(module_id)
        error('Unknown runtime module: ' + module_id)
    endif
endforeach

stage_tool = files('../tools/stage.py')
verification_tool = files('../tools/verify-runtime.py')
package_tool = files('../tools/package.py')
module_package_tool = files('../tools/package-module.py')
specification_tool = files('../tools/module-spec.py')
stage_inputs = [manager]
stage_arguments = []

foreach module_id, specification : module_specs
    inputs = specification['binaries']
    assets = specification['assets']
    description = []
    manifest = []
    foreach executable : specification['binaries']
        destination = 'bin' / fs.name(executable.full_path())
        description += [
            '--file', executable.full_path(), destination,
            '--executable', destination,
        ]
    endforeach
    foreach asset : assets
        destination = fs.relative_to(asset.full_path(), source_root / 'modules' / module_id)
        if destination.startswith('packaging/')
            destination = destination.substring(10)
        endif
        if destination == 'module.json.in'
            manifest = asset
        endif
        description += ['--file', asset.full_path(), destination]
    endforeach
    foreach data : specification.get('data', [])
        inputs += data['source']
        description += ['--verified-file', data['source'].full_path(), data['name']]
    endforeach
    foreach notice : specification['notices']
        description += ['--notice', notice]
    endforeach

    package_spec = configure_file(
        input: manifest,
        output: module_id + '-package.json',
        command: [
            python, specification_tool, 'describe',
            '--id', module_id,
            '--output', '@OUTPUT@',
        ] + description,
    )

    custom_target(
        'module-' + module_id,
        output: 'ocfusion-' + module_id + '-' + version + '-aarch64.tar.gz',
        input: inputs,
        depend_files: [
            package_spec, assets, common_notices, stage_tool,
            module_package_tool, package_tool, verification_tool,
            files('../LICENSE'), lock,
        ],
        build_by_default: false,
        command: [
            python, module_package_tool,
            '--spec', package_spec,
            '--version', version,
            '--output', '@OUTPUT@',
        ],
    )

    if module_id in selected_modules
        stage_inputs += inputs
        runtime_assets += assets
        stage_arguments += ['--spec', package_spec]
    endif
endforeach

# Dependencies come from runtime manifests, rather than a second build-time list.
selection = run_command(
    python, specification_tool, 'check-selection',
    stage_arguments,
    check: false,
)
if selection.returncode() != 0
    error(selection.stderr().strip())
endif

stage = custom_target(
    'runtime-stage',
    output: 'runtime-stage.stamp',
    input: stage_inputs,
    depend_files: [stage_tool, runtime_assets, common_notices, lock],
    command: [
        python, stage_tool,
        '--version', version,
        '--build-dir', build_root,
        '--manager', '@INPUT0@',
        '--output', '@OUTPUT@',
    ] + stage_arguments,
)

runtime = custom_target(
    'runtime',
    output: 'runtime.stamp',
    depends: stage,
    depend_files: [verification_tool, stage_tool, lock],
    build_by_default: true,
    command: [
        python, verification_tool,
        '--version', version,
        '--build-dir', build_root,
        '--output', '@OUTPUT@',
    ] + stage_arguments,
)

alias_target('build', runtime)
bundle = run_target(
    'bundle',
    depends: runtime,
    command: [
        python, package_tool,
        '--version', version,
        '--build-dir', build_root,
        '--output-dir', source_root / 'dist',
    ],
)
alias_target('package', bundle)
