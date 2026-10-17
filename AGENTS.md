# Working on OCFusion

OCFusion extends stock ASUSWRT without replacement firmware. Read
[the architecture](docs/architecture.md) before changing service ownership,
storage, startup or module integration. Follow
[the module contract](docs/modules.md) for module interfaces and
[build instructions](docs/building.md) for toolchain setup.

## Scope and source ownership

- Keep the repository and source archive source-only. Never commit or package
  `contrib/`, `PLAN.md`, downloaded archives, build outputs, firmware images or
  private router profiles.
- Pin dependencies by immutable version/commit and verified digest. Downloads
  happen during the build. Do not vendor generated binaries or proprietary ASUS
  assets.
- Derive versions from Git tags; use Meson's `release_version` override only for
  an explicitly agreed local build. Do not add a VERSION file or duplicate
  version counters.
- Runtime archive names include architecture, not router model. Source archive
  roots use `ocfusion-<version>/`.
- Document tested hardware only in README Compatibility. Architecture checks are
  not evidence that another ASUSWRT implementation works. Obtain display
  model/firmware from the router instead of hardcoding them.

## Implementation rules

- Keep all code, including Meson build files, readable and formatted. Separate
  logical blocks with blank lines, including within functions; indent case
  branches. Expand control structures across lines: never compress if/then/else,
  loops, case branches or similar blocks into a single line. Use functions with
  one clear responsibility; do not replace the Meson dependency graph with a
  monolithic build script.
- JavaScript conditions and loops always use braced blocks. Keep related
  declarations together, declare each variable separately and separate functions
  and logical steps with blank lines. Formatting applies to browser code, tests
  and inline scripts in examples.
- All module UIs use the manifest `ui` page, normal manager publishing and the
  same iframe/API. Do not add builtin-only panel fragments or module-ID
  exceptions to UI publishing.
- Keep protocol validation, credentials and client settings redaction inside
  OpenConnect. The VPN provider owns native Fusion/SDN, routing, firewall, DNS
  and connection leases. The platform owns shared web/stock transport and
  generic module lifecycle; do not reintroduce module-ID dispatch exceptions.
  The current format is the first release baseline. Do not add compatibility
  with prerelease installation formats. Preserve private profiles, Fusion
  indices and SDN bindings when updating this format. VPN resources belong to
  the provider and are addressed by module/profile owner, never allocated by
  client modules.
- Stock shell compatibility matters: use `/bin/sh`, POSIX syntax and fixed-path
  tool discovery. The inspected shell lacks `command`/`id`; another `sh`
  executable is a memory tool. Stock iptables lacks `-w`. Preserve required
  tun/xt_comment preparation.
- Avoid literal ASUS template delimiters in ordinary runtime JavaScript,
  including strings/comments: the stock web server renders native templates in
  scripts. Construct test markers from separate characters when needed.
- The web panel uses the existing ASUS server/session, CSS and language flow. Do
  not add a listener, bypass authentication or redistribute firmware assets.
  Keep stock-overlay mount ownership checks and a working direct panel URL.
  Integrate best effort with the current stock DOM; never pin firmware versions
  or stock file hashes.
- Do not return passwords, PEM bodies or raw secret-bearing errors to the
  browser. Keep request acknowledgement, size/hash bounds and revision checks
  intact.

## Dependencies and licenses

When adding or changing a dependency:

1. Verify the exact upstream source, version, hash and license from primary
   sources. Update the lock and all affected build inputs together.
2. Check how it is used: build tool, separate executable, linked library or
   data. For linked components, assess compatibility of the actual combination
   and linking mode, including upstream exceptions. Our GPL label does not
   relicense dependencies.
3. Preserve required attribution, license texts and source/relinking obligations
   in runtime distribution. A download URL and hash alone do not satisfy every
   corresponding-source obligation.
4. Apply and test tracked upstream patches against the pinned source. Patches
   retain the original project's license; do not insert OCFusion license
   preambles or create patch-specific license files.
5. Record unresolved release-specific findings in local PLAN.md, not a public
   audit document. Do not silently turn an unresolved assessment into a claim of
   compliance.

Own code is GPL-3.0-or-later. Add only the SPDX license identifier for
`GPL-3.0-or-later` to owned programming-language source and executable shell
scripts. Do not add SPDX headers to Markdown, HTML/CSS, metadata, build
definitions or upstream files. Do not create `.license` sidecars. README's final
section is License.

## Documentation and product text

- Development documentation is English. Product text has EN/RU parity, follows
  native ASUS language and falls back to EN. English is the source language for
  UI strings and translation dictionary keys; dictionary values provide Russian
  translations.
- Prefer the full name OCFusion without expanding it. The accepted short form is
  `ocf`. Use ASCII hyphens in documentation/UI.
- Keep public docs useful: installation/operation, build/test, architecture and
  module contract. Update the relevant document with interface changes; do not
  add agent diaries, dependency audits, speculative roadmaps or verification
  reports.
- Keep agent instructions here, system design in docs/architecture.md, and
  temporary plans/device observations in ignored PLAN.md. Avoid repeating the
  same facts in several documents.

## Validation

Use the pinned toolchain described in docs/building.md. Python uses the pinned
Ruff version: run `ruff check .` and `ruff format --check .`; use
`ruff format .` for formatting. Keep its rules aligned with the apt-mirror2
baseline. Typical checks:

```sh
meson setup build/meson --cross-file build-env/armv8-a-crypto-crc.ini
meson compile -C build/meson package
meson test -C build/meson --suite unit --suite example --suite client --print-errorlogs
```

For UI changes, configure a native build with
`-Druntime=false -Dbrowser_tests=true`, install the locked npm/browser
dependencies and run the browser suite. Go checks include gofmt, vet and race
tests. Use the repository's integration fixtures for installer, USB preparation
and module API changes. Native client-hook checks do not require QEMU.

Check source/archive exclusions, static stripped AArch64 output and archive
checksums when packaging changes. Run the incremental build-graph test when
changing build dependencies. Keep test coverage proportional to the change;
documentation-only edits need source/link and packaging checks, not a full
repeated runtime test suite.

State validation accurately: host fixtures do not prove physical router
behavior, and a native build does not prove Docker or remote Actions execution.
Follow the user's requested Git history/author and never include ignored working
notes in a commit.
