// Bundles bin/nami.js into the single-file launcher the release ships.
//
// Native addons stay external. Bundling a .node file fails outright — rolldown
// reads it as JavaScript and rejects it as invalid UTF-8 — and rewriting it to
// an asset path is worse, because the require then returns the path string and
// canvas crashes on boot trying to use it as its binding. Left external, the
// requires survive into the bundle and node resolves the addon at runtime from
// a node_modules directory beside the launcher.
export default {
  input: "bin/nami.js",
  platform: "node",
  external: [/\.node$/, /-darwin-arm64$/, /-darwin-x64$/, /-darwin-universal$/, /-linux-/, /-win32-/, /^fsevents$/],
  output: {
    format: "esm",
    inlineDynamicImports: true,
    file: "release/nami.js.raw",
  },
};
