import React from "react";
import { detectTerminalCaps, ThemeProvider } from "silvery";
import { createApp } from "silvery/runtime";
import { createTheme } from "silvery/theme";
import App from "./App.js";
import { installClipboardBridge } from "./utils/clipboardBridge.js";

installClipboardBridge();

const enginePath = process.env["CHAN_ENGINE_PATH"] ?? "chan-engine";
const model = process.env["CHAN_MODEL"] ?? "github-copilot/gpt-5.4";
const mode = process.env["CHAN_MODE"] ?? "plan";
const autoMode = process.env["CHAN_AUTO_MODE"] === "true";
const theme = createTheme()
  .preset("sonokai")
  .color("background", "#2C2E34")
  .color("foreground", "#E2E2E3")
  .color("cursorColor", "#E2E2E3")
  .color("cursorText", "#2C2E34")
  .color("selectionBackground", "#4A4C53")
  .color("selectionForeground", "#E2E2E3")
  .build();
const caps = detectTerminalCaps();

const app = createApp(() => () => ({}));
const handle = await app.run(
  <ThemeProvider theme={theme}>
    <App enginePath={enginePath} model={model} mode={mode} autoMode={autoMode} />
  </ThemeProvider>,
  {
    caps,
    alternateScreen: true,
    kitty: caps.kittyKeyboard,
    mouse: true,
    focusReporting: true,
    selection: true,
    textSizing: "auto",
    widthDetection: "auto",
  },
);
await handle.waitUntilExit();
