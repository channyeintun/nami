import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { useState, useEffect, useCallback, useRef } from "react";
import type {
  BackgroundAgentInspectPayload,
  BackgroundAgentStopPayload,
  BackgroundCommandInspectPayload,
  BackgroundCommandStopPayload,
  ModelSelectionResponsePayload,
  PermissionResponseDecision,
  RewindSelectionResponsePayload,
  ResumeSelectionResponsePayload,
  StreamEvent,
  UserInputImagePayload,
} from "../protocol/types.js";
import {
  parseEvent,
  serializeMessage,
  createMessage,
} from "../protocol/codec.js";
import type { ClientMessage } from "../protocol/types.js";

interface EngineState {
  ready: boolean;
  error: string | null;
}

interface EngineOptions {
  model?: string;
  mode?: string;
  autoMode?: boolean;
  onEvent?: (event: StreamEvent) => void;
}

export function useEngine(enginePath: string, options: EngineOptions = {}) {
  const [state, setState] = useState<EngineState>({
    ready: false,
    error: null,
  });
  const processRef = useRef<ChildProcess | null>(null);
  const onEventRef = useRef<EngineOptions["onEvent"]>(options.onEvent);

  useEffect(() => {
    onEventRef.current = options.onEvent;
  }, [options.onEvent]);

  useEffect(() => {
    const args = ["--stdio"];
    if (options.model) {
      args.push("--model", options.model);
    }
    if (options.mode) {
      args.push("--mode", options.mode);
    }
    if (options.autoMode) {
      args.push("--auto-mode");
    }

    const proc = spawn(enginePath, args, {
      stdio: ["pipe", "pipe", "pipe"],
    });
    processRef.current = proc;

    const rl = createInterface({ input: proc.stdout! });
    const stderrRl = createInterface({ input: proc.stderr! });

    rl.on("line", (line) => {
      const event = parseEvent(line);
      if (!event) return;

      onEventRef.current?.(event);

      setState((prev) => {
        const next = {
          ...prev,
        };
        if (event.type === "ready") {
          next.ready = true;
        }
        return next;
      });
    });

    stderrRl.on("line", (line) => {
      const message = line.trim();
      if (!message) return;
      // Only treat lines starting with "error" or "fatal" as real errors.
      // Other stderr output is diagnostic (debug logs, warnings).
      if (/^(error|fatal)/i.test(message)) {
        setState((prev) => ({
          ...prev,
          error: prev.error ?? message,
        }));
      }
    });

    proc.on("exit", (code) => {
      if (code !== 0) {
        setState((prev) => ({
          ...prev,
          error: `Engine exited with code ${code}`,
        }));
      }
    });

    return () => {
      rl.close();
      stderrRl.close();

      if (proc.stdin?.writable) {
        proc.stdin.write(serializeMessage(createMessage("shutdown")));
        proc.stdin.end();
      }

      const killTimer = setTimeout(() => {
        if (!proc.killed) {
          proc.kill("SIGTERM");
        }
      }, 250);

      const forceKillTimer = setTimeout(() => {
        if (!proc.killed) {
          proc.kill("SIGKILL");
        }
      }, 1000);

      proc.once("close", () => {
        clearTimeout(killTimer);
        clearTimeout(forceKillTimer);
      });
    };
  }, [enginePath, options.mode, options.model]);

  const send = useCallback((msg: ClientMessage) => {
    const proc = processRef.current;
    if (proc?.stdin?.writable) {
      proc.stdin.write(serializeMessage(msg));
    }
  }, []);

  const sendInput = useCallback(
    (text: string, images?: UserInputImagePayload[]) =>
      send(
        createMessage("user_input", {
          text,
          images,
        }),
      ),
    [send],
  );

  const sendCommand = useCallback(
    (command: string, args?: string) =>
      send(createMessage("slash_command", { command, args: args ?? "" })),
    [send],
  );

  const sendCancel = useCallback(() => send(createMessage("cancel")), [send]);
  const sendModeToggle = useCallback(
    () => send(createMessage("mode_toggle")),
    [send],
  );
  const sendShutdown = useCallback(
    () => send(createMessage("shutdown")),
    [send],
  );

  const sendPermissionResponse = useCallback(
    (
      requestId: string,
      decision: PermissionResponseDecision,
      feedback?: string,
    ) =>
      send(
        createMessage("permission_response", {
          request_id: requestId,
          decision,
          feedback,
        }),
      ),
    [send],
  );

  const sendArtifactReviewResponse = useCallback(
    (requestId: string, decision: string, feedback?: string) =>
      send(
        createMessage("artifact_review_response", {
          request_id: requestId,
          decision,
          feedback,
        }),
      ),
    [send],
  );

  const sendResumeSelectionResponse = useCallback(
    (payload: ResumeSelectionResponsePayload) =>
      send(createMessage("resume_selection_response", payload)),
    [send],
  );

  const sendRewindSelectionResponse = useCallback(
    (payload: RewindSelectionResponsePayload) =>
      send(createMessage("rewind_selection_response", payload)),
    [send],
  );

  const sendModelSelectionResponse = useCallback(
    (payload: ModelSelectionResponsePayload) =>
      send(createMessage("model_selection_response", payload)),
    [send],
  );

  const sendBackgroundCommandInspect = useCallback(
    (payload: BackgroundCommandInspectPayload) =>
      send(createMessage("background_command_inspect", payload)),
    [send],
  );

  const sendBackgroundCommandStop = useCallback(
    (payload: BackgroundCommandStopPayload) =>
      send(createMessage("background_command_stop", payload)),
    [send],
  );

  const sendBackgroundAgentInspect = useCallback(
    (payload: BackgroundAgentInspectPayload) =>
      send(createMessage("background_agent_inspect", payload)),
    [send],
  );

  const sendBackgroundAgentStop = useCallback(
    (payload: BackgroundAgentStopPayload) =>
      send(createMessage("background_agent_stop", payload)),
    [send],
  );

  return {
    ...state,
    sendInput,
    sendCommand,
    sendCancel,
    sendModeToggle,
    sendShutdown,
    sendPermissionResponse,
    sendArtifactReviewResponse,
    sendModelSelectionResponse,
    sendRewindSelectionResponse,
    sendResumeSelectionResponse,
    sendBackgroundCommandInspect,
    sendBackgroundCommandStop,
    sendBackgroundAgentInspect,
    sendBackgroundAgentStop,
  };
}
