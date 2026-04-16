import React from "react";
import { detectTerminalCaps, ThemeProvider } from "silvery";
import { createApp } from "silvery/runtime";
import { presetTheme, type Theme } from "silvery/theme";
import App from "./App.js";

const enginePath = process.env["CHAN_ENGINE_PATH"] ?? "chan-engine";
const model = process.env["CHAN_MODEL"] ?? "anthropic/claude-sonnet-4-20250514";
const mode = process.env["CHAN_MODE"] ?? "plan";
const theme: Theme = presetTheme("nord");
const caps = detectTerminalCaps();

const app = createApp(() => () => ({}));
const handle = await app.run(
	<ThemeProvider theme={theme}>
		<App enginePath={enginePath} model={model} mode={mode} />
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
