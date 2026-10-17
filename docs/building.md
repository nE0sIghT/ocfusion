# Build and test

## Inputs and tools

OCFusion uses Meson 1.12.1 and Ninja 1.13.2 to orchestrate the Go services,
static C dependencies, web assets and packaging. Go 1.27.2 builds the router
services; GCC 14.2.0 cross-compiles C for AArch64. All runtime dependencies are
fetched by immutable URL and verified against `third_party/sources.lock.json`.
No firmware image, prebuilt router executable or MVP directory is required.

Install Go 1.27.2, native C/C++ compilers, AArch64 GCC 14.2.0 and its
binutils/Linux headers, make, autoconf, automake, libtool, pkg-config, Python
3.12 or newer, Perl, patch, xz and Node.js. The cross compiler must be available
as `aarch64-linux-gnu-gcc`. The matching Debian packages are listed in
`build-env/Dockerfile`. If cross headers are installed outside
`/usr/aarch64-linux-gnu/include`, set `OCF_KERNEL_HEADERS` to their include
directory.

On an x86-64 Linux computer, install the hash-pinned build tools:

```sh
python3 -m venv .venv
.venv/bin/pip install --require-hashes --only-binary=:all: -r build-env/requirements.txt
. .venv/bin/activate
```

The wheel hashes cover the supported x86-64 Linux build environment. On another
host architecture, the Meson wheel is portable but the Ninja wheel hash must be
reviewed for that platform.

## Meson build

```sh
meson setup build/meson --cross-file build-env/armv8-a-crypto-crc.ini
meson compile -C build/meson
meson compile -C build/meson bundle
meson test -C build/meson --suite unit --suite example --suite client --print-errorlogs
```

The root `meson.build` configures the project and includes component build files
from `third_party/`, `platform/`, `modules/vpn/`, `modules/openconnect/`,
`modules/adguard-home/`, `modules/radio-settings/`, `packaging/` and `tests/`.
Upstream configure options, compiler environment and install targets are in
[third_party/build.json](../third_party/build.json); pinned downloads remain in
`sources.lock.json`.

Meson owns the dependency graph. Separate targets build musl, zlib, OpenSSL,
libxml2, OpenConnect, the supervisor and the module manager. A small adapter
invokes the original upstream configure/make commands for one C project at a
time. It does not orchestrate other projects. The OpenConnect target applies its
tracked patches before compilation. AdGuard Home is built from the locked
upstream archive and its go.sum-pinned dependencies with private-control and
DNS-dialer patches. Its native DNS integration fixture is also built from source
for the client suite. Dependency licenses are collected into the runtime
notices. Upstream build logs are in `build/meson/logs/`.

Changing supervisor Go source rebuilds only that executable and runtime staging.
Changing the OpenConnect patch rebuilds the client and staging, while preserving
its libraries. Updating the CA input restages the trust bundle without
recompiling C. A repeated unchanged compile does no work. Verify these
invariants after a complete build:

```sh
python3 tests/test-build-graph.py --build-dir build/meson
```

The cross file describes Linux/AArch64 and the confirmed ARMv8 AES/PMULL/CRC
capabilities. C uses `-O2 -march=armv8-a+crypto+crc -fstack-protector-strong`
and OpenSSL AArch64 assembly. Go uses CGO_ENABLED=0, buildvcs=false, trimpath
and stripped output. ELF validation requires static AArch64 binaries. Tests run
natively; QEMU is not used.

Native tests also work without cross-compilation tools:

```sh
meson setup build/tests -Druntime=false -Dbrowser_tests=true
meson test -C build/tests --suite unit --suite example --print-errorlogs
```

Python checks use Ruff 0.16.6 from the pinned build requirements. Run the same
lint and formatting checks as PR CI:

```sh
ruff check .
ruff format --check .
```

Use `ruff format .` to format Python files. The lint baseline follows
[apt-mirror2](https://github.com/nE0sIghT/apt-mirror2/blob/master/pyproject.toml).

Go checks include gofmt, go vet and race-enabled tests. golangci-lint is not
currently a required tool.

## Browser tests

```sh
npm ci
npx playwright install --with-deps chromium
meson test -C build/tests --suite browser --print-errorlogs
```

Playwright 1.64.0 is pinned in the npm lock. Browser fixtures contain no
proprietary ASUS firmware assets. Optional native-header checks may use a
separately supplied inspected state.js through `ASUS_STATE_JS_FIXTURE`; it is
never distributed.

## Optional container environment

Docker provides the build tools when you prefer not to install them on the host.
It is not required on the router or for a correctly provisioned native host. The
Dockerfile extends one official `golang:1.27.2-trixie` image, pinned by digest,
with the cross compiler and hash-pinned Meson/Ninja. The Debian snapshot fixes
the remaining package inputs.

```sh
docker build -t ocfusion-build -f build-env/Dockerfile .
docker run --rm -v "$PWD:/work" -w /work ocfusion-build bash -ec '
  meson setup build/meson --cross-file build-env/armv8-a-crypto-crc.ini
  meson compile -C build/meson bundle
  meson test -C build/meson --suite unit --suite example --suite client --print-errorlogs
'
```

Keep a build directory associated with its compiler environment; use a separate
directory or `meson setup --wipe` when switching hosts/toolchains. The container
is the controlled CI build environment.

The unit suite also checks updates from `v0.3.1` using native manager builds
from that tag and the current sources. It relocates filesystem paths, UID/PATH
checks and NVRAM commands into host fixtures; it does not modify the host's
router settings. This historical test skips when the released tag is
unavailable, such as in an extracted source archive.

## Outputs, reproducibility and CI

Release versions come from Git tags such as `v0.4`. Untagged commits use a
development suffix. For a local test build or an extracted source archive
without Git metadata, pass `-Drelease_version=0.4` to `meson setup`. The full
source commit is embedded in owned executables and shown in the panel System
section. Executable version output uses its first 7 hexadecimal characters.
Source archives retain it in generated `.git-revision` metadata so rebuilding
without Git preserves this identifier.

The resolved version supplies all executables and archive names; no tracked
version file needs updating. Runtime staging is under
`build/meson/runtime/ocfusion`; source templates remain unchanged. The `bundle`
target creates in `dist/`:

- `ocfusion-0.4-aarch64.tar.gz`
- `ocfusion-0.4-source.tar.gz`, with project sources, patches and dependency
  locks
- `SHA256SUMS`

Downloads, build trees, firmware, PLAN.md and contrib are excluded from the
source archive. The CA snapshot is Mozilla's trust store converted by the
official curl project, not a separate curl trust policy.

SOURCE_DATE_EPOCH controls sorted archives with normalized ownership, modes and
gzip timestamps. Packaging is deterministic. C compiler output can embed build
paths, so arbitrary host directories are not claimed to produce byte-identical
binaries; use the controlled `/work` container path for comparisons.
`meson compile -C build/meson --clean` removes graph outputs;
`meson setup --wipe` recreates configuration. Deleting `build/meson` removes the
whole C sysroot and extracted-source cache too.

PRs run Meson host/browser checks, a source-built ARM64 bundle and incremental
graph checks. Tags `v*` run the same workflow, derive the version from the
release tag and publish a release with archives/checksums. Actions are pinned by
full commit SHA.

The source archive extracts into `ocfusion-<version>/` (for example,
`ocfusion-0.4/`), following the conventional package-name/version layout.

## Build commands

Configure a build directory once with `meson setup`. Named targets reuse its
configuration; there are no wrapper-specific directory, platform or version
defaults.

| Command                                     | Purpose                                                                   |
| ------------------------------------------- | ------------------------------------------------------------------------- |
| `meson compile -C build/meson build`        | Build the router runtime; also the default compile target.                |
| `meson compile -C build/meson package`      | Build and package the runtime and source archives; alias of `bundle`.     |
| `meson compile -C build/tests test-unit`    | Run the unit suite, run directly from the configured build.               |
| `meson compile -C build/tests test-example` | Run the module example suite.                                             |
| `meson compile -C build/tests test-browser` | Run browser tests; available when configured with `-Dbrowser_tests=true`. |
| `meson test -C build/tests`                 | Run all enabled suites using Meson's standard test runner.                |
| `meson compile -C build/meson --clean`      | Clean graph outputs while retaining configuration.                        |

The custom `package` target creates the documented release archives; it is
separate from Meson's built-in `meson dist` command. Named test targets use
`--no-rebuild` to avoid recursively invoking Ninja. Tests run directly from
source and require the dependencies described above. To discard configuration
and downloaded build inputs entirely, remove the build directory. Release
archives in `dist/` are separate packaging outputs.
