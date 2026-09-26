import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { useState, useEffect, useCallback, useRef } from "react";
import type {
  AskUserQuestionResponsePayload,
  BackgroundAgentInspectPayload,
  BackgroundAgentStopPayload,
  BackgroundCommandInspectPayload,
  BackgroundCommandStopPayload,
  ModelSelectionResponsePayload,
  PermissionResponseDecision,
  ReasoningSelectionResponsePayload,
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
    // Set once cleanup starts; from then on the engine going away is expected.
    let stopping = false;

    // Both of these are emitted as "error" events, which crash the process
    // when nothing listens: a binary that is missing or not executable (no
    // "exit" follows), and a write racing the engine's exit (EPIPE).
    proc.on("error", (err) => {
      if (stopping) return;
      setState((prev) => ({
        ...prev,
        error: `Could not start engine: ${err.message}`,
      }));
    });
    proc.stdin?.on("error", (err) => {
      if (stopping) return;
      setState((prev) => ({
        ...prev,
        error: prev.error ?? `Lost connection to engine: ${err.message}`,
      }));
    });

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

    // The first error line on stderr is usually why the engine is exiting, so
    // the exit report below carries it.
    let stderrError: string | null = null;

    stderrRl.on("line", (line) => {
      const message = line.trim();
      if (!message || stderrError) return;
      // Only treat lines starting with "error", "fatal" or "panic" (a Go
      // panic) as real errors. Other stderr output is diagnostic (debug logs,
      // warnings).
      if (/^(error|fatal|panic)/i.test(message)) {
        stderrError = message;
        setState((prev) => ({
          ...prev,
          error: prev.error ?? message,
        }));
      }
    });

    // "close" rather than "exit" so stderr has been read to the end. Any exit
    // before cleanup is unexpected, including status 0: the UI is left
    // talking to nothing either way.
    proc.on("close", (code, signal) => {
      // A spawn that failed has no pid and was reported as an "error".
      if (stopping || proc.pid === undefined) return;
      const exit = signal
        ? `Engine was killed by ${signal}`
        : `Engine exited with code ${code}`;
      setState((prev) => ({
        ...prev,
        error: stderrError ? `${exit}: ${stderrError}` : exit,
      }));
    });

    return () => {
      stopping = true;
      rl.close();
      stderrRl.close();

      if (proc.stdin?.writable) {
        proc.stdin.write(serializeMessage(createMessage("shutdown")));
        proc.stdin.end();
      }

      // Not proc.killed: that only records that a signal was sent, so it
      // turned true at the SIGTERM and the SIGKILL never followed.
      const hasExited = () =>
        proc.exitCode !== null || proc.signalCode !== null;
      if (hasExited()) return;

      const killTimer = setTimeout(() => {
        if (!hasExited()) {
          proc.kill("SIGTERM");
        }
      }, 250);

      const forceKillTimer = setTimeout(() => {
        if (!hasExited()) {
          proc.kill("SIGKILL");
        }
      }, 1000);

      proc.once("exit", () => {
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

  const sendAskUserQuestionResponse = useCallback(
    (payload: AskUserQuestionResponsePayload) =>
      send(createMessage("ask_user_question_response", payload)),
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

  const sendReasoningSelectionResponse = useCallback(
    (payload: ReasoningSelectionResponsePayload) =>
      send(createMessage("reasoning_selection_response", payload)),
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

  const sendSwarmDashboardInspect = useCallback(
    () => send(createMessage("swarm_dashboard_inspect", {})),
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
    sendAskUserQuestionResponse,
    sendArtifactReviewResponse,
    sendModelSelectionResponse,
    sendReasoningSelectionResponse,
    sendRewindSelectionResponse,
    sendResumeSelectionResponse,
    sendBackgroundCommandInspect,
    sendBackgroundCommandStop,
    sendBackgroundAgentInspect,
    sendBackgroundAgentStop,
    sendSwarmDashboardInspect,
  };
}
