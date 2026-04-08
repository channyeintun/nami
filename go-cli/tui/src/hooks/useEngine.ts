import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { useState, useEffect, useCallback, useRef } from "react";
import type { StreamEvent } from "../protocol/types.js";
import { parseEvent, serializeMessage, createMessage } from "../protocol/codec.js";
import type { ClientMessage } from "../protocol/types.js";

interface EngineState {
  ready: boolean;
  events: StreamEvent[];
  error: string | null;
}

export function useEngine(enginePath: string) {
  const [state, setState] = useState<EngineState>({
    ready: false,
    events: [],
    error: null,
  });
  const processRef = useRef<ChildProcess | null>(null);

  useEffect(() => {
    const proc = spawn(enginePath, ["--stdio"], {
      stdio: ["pipe", "pipe", "pipe"],
    });
    processRef.current = proc;

    const rl = createInterface({ input: proc.stdout! });

    rl.on("line", (line) => {
      const event = parseEvent(line);
      if (!event) return;

      setState((prev) => {
        const next = { ...prev, events: [...prev.events, event] };
        if (event.type === "ready") {
          next.ready = true;
        }
        if (event.type === "error") {
          next.error = (event.payload as { message: string })?.message ?? "Unknown error";
        }
        return next;
      });
    });

    proc.on("exit", (code) => {
      if (code !== 0) {
        setState((prev) => ({ ...prev, error: `Engine exited with code ${code}` }));
      }
    });

    return () => {
      proc.kill("SIGTERM");
    };
  }, [enginePath]);

  const send = useCallback((msg: ClientMessage) => {
    const proc = processRef.current;
    if (proc?.stdin?.writable) {
      proc.stdin.write(serializeMessage(msg));
    }
  }, []);

  const sendInput = useCallback(
    (text: string) => send(createMessage("user_input", { text })),
    [send]
  );

  const sendCommand = useCallback(
    (command: string, args?: string) =>
      send(createMessage("slash_command", { command, args: args ?? "" })),
    [send]
  );

  const sendCancel = useCallback(() => send(createMessage("cancel")), [send]);
  const sendModeToggle = useCallback(() => send(createMessage("mode_toggle")), [send]);
  const sendShutdown = useCallback(() => send(createMessage("shutdown")), [send]);

  const sendPermissionResponse = useCallback(
    (requestId: string, decision: "allow" | "deny" | "always_allow") =>
      send(createMessage("permission_response", { request_id: requestId, decision })),
    [send]
  );

  return {
    ...state,
    sendInput,
    sendCommand,
    sendCancel,
    sendModeToggle,
    sendShutdown,
    sendPermissionResponse,
  };
}
