# Third-party inputs

`sources.lock.json` pins upstream source archives and the Mozilla CA bundle by URL and SHA-256. Builds download and verify these inputs; downloaded implementations are excluded from the repository and source archive. `build.json` contains the configure commands, compiler environment, make targets, patches and license-file locations for upstream C builds. Meson defines dependency ordering. Source versions come from the locked archive names rather than the recipes.

Build-tool pins live in `build-env/`, the npm lock and workflow definitions.

`notices/` supplies the Go, GCC-runtime and Mozilla license texts required by runtime packaging. License notices for C dependencies are copied from their verified upstream archives during the build. These notices cover redistributed components, not separate licenses for OCFusion-owned files. ASUS web assets are referenced on the router and are not redistributed.
