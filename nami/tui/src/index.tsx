import React from "react";
import { createTerminalProfile, ThemeProvider } from "silvery";
import { createApp } from "silvery/runtime";
import { createTheme } from "silvery/theme";
import App from "./App.js";
import { installClipboardBridge } from "./utils/clipboardBridge.js";

installClipboardBridge();

const enginePath = process.env["NAMI_ENGINE_PATH"] ?? "nami-engine";
const model = process.env["NAMI_MODEL"] ?? "github-copilot/gpt-5.4";
const mode = process.env["NAMI_MODE"] ?? "plan";
const autoMode = process.env["NAMI_AUTO_MODE"] === "true";
const theme = createTheme()
  .preset("sonokai")
  .color("background", "#2C2E34")
  .color("foreground", "#E2E2E3")
  .color("cursorColor", "#E2E2E3")
  .color("cursorText", "#2C2E34")
  .color("selectionBackground", "#4A4C53")
  .color("selectionForeground", "#E2E2E3")
  .build();
const profile = createTerminalProfile();

const app = createApp(() => () => ({}));
const handle = await app.run(
  <ThemeProvider theme={theme}>
    <App enginePath={enginePath} model={model} mode={mode} autoMode={autoMode} />
  </ThemeProvider>,
  {
    profile,
    alternateScreen: true,
    kitty: profile.caps.kittyKeyboard,
    focusReporting: true,
    handleTabCycling: false,
    textSizing: "auto",
    widthDetection: "auto",
  },
);
await handle.waitUntilExit();
