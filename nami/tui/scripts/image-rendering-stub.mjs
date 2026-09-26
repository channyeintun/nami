// Stands in for @napi-rs/canvas and @resvg/resvg-js in the launcher bundle.
//
// silvery depends on @termless/core, which imports @napi-rs/canvas as soon as
// it loads and @resvg/resvg-js when asked for a PNG. Both are native addons
// that termless uses only to render terminal screenshots, which the TUI never
// takes, and a release installs nami.js without a node_modules directory they
// could load from. rolldown.config.mjs resolves both packages to this module
// instead: importing it does nothing, and anything that does reach for image
// rendering fails with an error that says why. A static import of a name this
// module lacks fails the build with MISSING_EXPORT, so a termless upgrade that
// needs more of either package shows up there.

function imageRenderingUnavailable(packageName) {
  throw new Error(
    `${packageName} is not part of the nami launcher bundle: the TUI does not render images, so the bundle leaves this native addon out.`,
  );
}

// What @termless/core uses from @napi-rs/canvas.
export const GlobalFonts = {
  registerFromPath() {
    imageRenderingUnavailable("@napi-rs/canvas");
  },
};

export function createCanvas() {
  imageRenderingUnavailable("@napi-rs/canvas");
}

export class Image {
  constructor() {
    imageRenderingUnavailable("@napi-rs/canvas");
  }
}

// What @termless/core uses from @resvg/resvg-js.
export class Resvg {
  constructor() {
    imageRenderingUnavailable("@resvg/resvg-js");
  }
}
