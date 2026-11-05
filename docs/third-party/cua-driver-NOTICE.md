# Bundled Cua Driver

AIClaw embeds the unmodified `@trycua/cua-driver` 0.38.0 SDK and the matching
platform-specific native library. Their versions and npm SHA-512 integrity
values are pinned in `package-lock.json`. They load inside AIClaw, without
connecting to an independently installed Cua daemon.

Cua Driver source and release:
https://github.com/trycua/cua/tree/cua-driver-rs-v0.38.0/libs/cua-driver

The Driver is MIT-licensed. See CUA-DRIVER-LICENSE.txt. AIClaw does not bundle
Cua Spaces, Driver Bench or the optional perception/model extension.

The generated Node bindings use `@ubjs/core` and `@ubjs/node` 0.31.0-3 under
MPL-2.0. The native package's `cua_driver_node_runtime.node` is a compatibility
build derived from that same runtime, also under MPL-2.0. Its upstream notice
is retained beside the binary. See MPL-2.0.txt.

Corresponding source for the runtime and bindings:
https://github.com/jhugman/uniffi-bindgen-react-native/tree/0.31.0-3

The exact runtime dependency and deterministic compatibility transformations
are published with Cua Driver 0.38.0:
https://github.com/trycua/cua/blob/cua-driver-rs-v0.38.0/libs/cua-driver/scripts/build-node-runtime.mjs

AIClaw makes no modifications to these MPL-covered files. Their source headers
and the JS/TypeScript source files distributed in the npm packages are retained.
