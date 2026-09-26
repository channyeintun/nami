// Bundles bin/nami.js into the single-file launcher the release ships.
//
// @napi-rs/canvas and @resvg/resvg-js resolve to a stub. silvery reaches them
// through @termless/core, which loads @napi-rs/canvas at startup for terminal
// screenshots the TUI never takes, and a release installs nami.js without a
// node_modules directory for these native addons: with the real packages the
// TUI stops at startup with "Cannot find native binding".
//
// Any other native addon stays external. Bundling a .node file fails outright —
// rolldown reads it as JavaScript and rejects it as invalid UTF-8 — and
// rewriting it to an asset path is worse, because the require then returns the
// path string instead of the binding. An external addon still needs a
// node_modules directory at runtime, which a release does not ship, so nothing
// the TUI loads at startup may require one.
import { fileURLToPath } from "node:url";

const imageRenderingStub = fileURLToPath(
  new URL("./scripts/image-rendering-stub.mjs", import.meta.url),
);

export default {
  input: "bin/nami.js",
  platform: "node",
  resolve: {
    alias: {
      "@napi-rs/canvas": imageRenderingStub,
      "@resvg/resvg-js": imageRenderingStub,
    },
  },
  external: [/\.node$/, /-darwin-arm64$/, /-darwin-x64$/, /-darwin-universal$/, /-linux-/, /-win32-/, /^fsevents$/],
  output: {
    format: "esm",
    inlineDynamicImports: true,
    file: "release/nami.js.raw",
  },
};
