import React from "react";
import { render } from "ink";
import App from "./App.js";

const enginePath = process.env["GOCODE_ENGINE_PATH"] ?? "gocode-engine";
const model = process.env["GOCODE_MODEL"] ?? "anthropic/claude-sonnet-4-20250514";
const mode = process.env["GOCODE_MODE"] ?? "plan";

render(<App enginePath={enginePath} model={model} mode={mode} />);
