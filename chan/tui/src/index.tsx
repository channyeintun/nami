import React from "react";
import { render } from "ink";
import App from "./App.js";

const enginePath = process.env["CHAN_ENGINE_PATH"] ?? "chan-engine";
const model = process.env["CHAN_MODEL"] ?? "anthropic/claude-sonnet-4-20250514";
const mode = process.env["CHAN_MODE"] ?? "plan";

render(<App enginePath={enginePath} model={model} mode={mode} />);
