import React, {
  type FC,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Box, Spinner, Text, useInput } from "silvery";
import { usePaste } from "silvery/runtime";
import { DEFAULT_PROMPT_MARKER } from "../constants/prompt.js";
import type { PromptController } from "../hooks/usePromptHistory.js";
import type { UISlashCommand } from "../hooks/useEvents.js";
import { useSlashCommandPreview } from "../hooks/useSlashCommandPreview.js";
import { parsePasteParts, type PastedImageData } from "../utils/imagePaste.js";
import SlashCommandPreview from "./SlashCommandPreview.js";
import ShimmerText from "./ShimmerText.js";

interface InputProps {
  prompt: PromptController;
  mode: string;
  slashCommands: UISlashCommand[];
  isLoading: boolean;
  statusLabel?: string | null;
  onSubmit: (overrideText?: string) => void;
  onOpenTranscriptSearch: () => void;
  onImagePaste: (images: PastedImageData[]) => void;
  onPasteWarning: (warnings: string[]) => void;
  onModeToggle: () => void;
  onThinkingVisibilityToggle: () => void;
  onArtifactVisibilityToggle: () => void;
  onReasoningToggle: () => void;
  onBackgroundTasksToggle: () => void;
  onRevealFooterHints: () => void;
  onSendQueuedPromptNow: () => void;
  onRemoveQueuedPrompt: () => void;
  onCancel: () => void;
  disabled?: boolean;
}

// Reserve enough room for the border, padding, and "> " prompt marker while
// still leaving a minimally usable wrapped editor width on narrow terminals.
const PROMPT_CHROME_COLUMNS = 8;
const MIN_PROMPT_TEXT_COLUMNS = 8;
const SUBMIT_DEBOUNCE_MS = 40;

function getPromptTextColumns(terminalColumns: number): number {
  return Math.max(
    MIN_PROMPT_TEXT_COLUMNS,
    terminalColumns - PROMPT_CHROME_COLUMNS,
  );
}

function renderInputLines(
  value: string,
  cursorOffset: number,
  columns: number,
): string[] {
  // Leave one column for the block cursor so a cursor rendered at the visual end
  // of a wrapped line does not spill onto an extra phantom segment.
  const wrapWidth = Math.max(1, columns - 1);
  const logicalLines = value.split("\n");
  const renderedLines: string[] = [];
  let lineStartOffset = 0;

  logicalLines.forEach((line, logicalLineIndex) => {
    if (line.length === 0) {
      const isCursorHere = cursorOffset === lineStartOffset;
      renderedLines.push(isCursorHere ? "█" : " ");
    } else {
      for (let start = 0; start < line.length; start += wrapWidth) {
        const end = Math.min(line.length, start + wrapWidth);
        const segmentStart = lineStartOffset + start;
        const segmentEnd = lineStartOffset + end;
        const isLastWrappedSegment = end === line.length;
        // A cursor on a wrap boundary belongs to the start of the next
        // segment; only the line's last segment shows it after its final
        // character, in the column wrapWidth keeps free for it.
        const isCursorInside =
          (cursorOffset >= segmentStart && cursorOffset < segmentEnd) ||
          (cursorOffset === segmentEnd && isLastWrappedSegment);

        if (!isCursorInside) {
          renderedLines.push(line.slice(start, end));
          continue;
        }

        const cursorColumn = cursorOffset - segmentStart;
        const rendered =
          line.slice(start, start + cursorColumn) +
          "█" +
          line.slice(start + cursorColumn, end);
        renderedLines.push(rendered);
      }
    }

    lineStartOffset += line.length;
    if (logicalLineIndex < logicalLines.length - 1) {
      lineStartOffset += 1;
    }
  });

  return renderedLines.length > 0 ? renderedLines : ["█"];
}

function formatPromptStatusLabel(statusLabel?: string | null): string {
  if (!statusLabel || statusLabel === "Thinking") {
    return "Working";
  }

  return statusLabel;
}

const Input: FC<InputProps> = ({
  prompt,
  mode,
  slashCommands,
  isLoading,
  statusLabel,
  onSubmit,
  onOpenTranscriptSearch,
  onImagePaste,
  onPasteWarning,
  onModeToggle,
  onThinkingVisibilityToggle,
  onArtifactVisibilityToggle,
  onReasoningToggle,
  onBackgroundTasksToggle,
  onRevealFooterHints,
  onSendQueuedPromptNow,
  onRemoveQueuedPrompt,
  onCancel,
  disabled,
}) => {
  const [terminalColumns, setTerminalColumns] = useState(
    process.stdout.columns ?? 80,
  );
  const submitTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearPendingSubmit = useCallback(() => {
    if (submitTimerRef.current === null) {
      return false;
    }

    clearTimeout(submitTimerRef.current);
    submitTimerRef.current = null;
    return true;
  }, []);

  const convertPendingSubmitToNewline = useCallback(() => {
    if (!clearPendingSubmit()) {
      return false;
    }

    prompt.insertNewline();
    return true;
  }, [clearPendingSubmit, prompt]);

  const flushPendingSubmit = useCallback(() => {
    if (!clearPendingSubmit()) {
      return false;
    }

    onSubmit();
    return true;
  }, [clearPendingSubmit, onSubmit]);

  const scheduleSubmit = useCallback(() => {
    clearPendingSubmit();
    submitTimerRef.current = setTimeout(() => {
      submitTimerRef.current = null;
      onSubmit();
    }, SUBMIT_DEBOUNCE_MS);
  }, [clearPendingSubmit, onSubmit]);

  const ensurePendingSubmitDoesNotSwallowPaste = useCallback(
    (text: string) => {
      if (submitTimerRef.current === null || text.length === 0) {
        return;
      }

      convertPendingSubmitToNewline();
    },
    [convertPendingSubmitToNewline],
  );

  useEffect(() => {
    const handleResize = () => {
      setTerminalColumns(process.stdout.columns ?? 80);
    };

    handleResize();
    process.stdout.on("resize", handleResize);

    return () => {
      process.stdout.off("resize", handleResize);
    };
  }, []);

  useEffect(() => {
    return () => {
      clearPendingSubmit();
    };
  }, [clearPendingSubmit]);

  const promptTextColumns = useMemo(
    () => getPromptTextColumns(terminalColumns),
    [terminalColumns],
  );
  const hasPromptDraft = prompt.value.length > 0;
  const slashPreview = useSlashCommandPreview({
    value: prompt.value,
    cursorOffset: prompt.cursorOffset,
    slashCommands,
  });

  const handleParsedPaste = React.useCallback(
    (
      text: string,
      options?: {
        warnIfClipboardImageMissing?: boolean;
      },
    ) => {
      if (disabled) {
        return;
      }

      ensurePendingSubmitDoesNotSwallowPaste(text);

      // A rejection here would reach silvery's unhandledRejection handler,
      // which tears the whole app down, so report it as a paste warning.
      parsePasteParts(text)
        .then((parts) => {
          if (parts.text.length > 0) {
            prompt.insertText(parts.text);
          }
          if (parts.images.length > 0) {
            onImagePaste(parts.images);
          }
          if (
            options?.warnIfClipboardImageMissing &&
            parts.images.length === 0 &&
            parts.text.length === 0 &&
            parts.warnings.length === 0
          ) {
            onPasteWarning(["No image found in clipboard"]);
            return;
          }
          onPasteWarning(parts.warnings);
        })
        .catch((error: unknown) => {
          const reason = error instanceof Error ? error.message : String(error);
          onPasteWarning([`Paste failed: ${reason}`]);
        });
    },
    [disabled, onImagePaste, onPasteWarning, prompt],
  );

  useInput(
    (input, key) => {
      const text = key.text ?? input;

      if (key.escape) {
        clearPendingSubmit();
        if (slashPreview.visible) {
          prompt.clear();
          return;
        }

        onCancel();
        return;
      }

      if ((key.meta && input?.toLowerCase() === "t") || text === "†") {
        flushPendingSubmit();
        onThinkingVisibilityToggle();
        return;
      }

      if (key.meta && input?.toLowerCase() === "a") {
        flushPendingSubmit();
        onArtifactVisibilityToggle();
        return;
      }

      if ((key.meta && input?.toLowerCase() === "r") || text === "®") {
        flushPendingSubmit();
        onReasoningToggle();
        return;
      }

      if (key.meta && input?.toLowerCase() === "b") {
        flushPendingSubmit();
        onBackgroundTasksToggle();
        return;
      }

      if (key.ctrl && input === "y") {
        flushPendingSubmit();
        onSendQueuedPromptNow();
        return;
      }

      if (key.ctrl && input === "k") {
        flushPendingSubmit();
        onRemoveQueuedPrompt();
        return;
      }

      if (!key.ctrl && !key.meta && text === "?") {
        onRevealFooterHints();
        if (prompt.value.length === 0 && !slashPreview.visible) {
          return;
        }
      }

      if (disabled) return;

      if (process.platform === "darwin" && key.ctrl && input === "v") {
        handleParsedPaste("", { warnIfClipboardImageMissing: true });
        return;
      }

      if (key.tab) {
        flushPendingSubmit();
        if (slashPreview.visible) {
          const nextValue = slashPreview.applySelection();
          if (nextValue) {
            prompt.setValue(nextValue);
          }
          return;
        }

        onModeToggle();
        return;
      }
      if (key.return) {
        if (key.shift || key.meta) {
          clearPendingSubmit();
          prompt.insertNewline();
          return;
        }

        if (slashPreview.visible && slashPreview.selectedCommand) {
          clearPendingSubmit();
          const nextValue = slashPreview.applySelection();
          if (nextValue) {
            prompt.setValue(nextValue);
          }
          if (!slashPreview.selectedCommand.takesArguments) {
            onSubmit(nextValue ?? undefined);
          }
          return;
        }

        scheduleSubmit();
        return;
      }
      if (key.upArrow) {
        flushPendingSubmit();
        if (slashPreview.visible) {
          slashPreview.selectPrevious();
          return;
        }

        if (!hasPromptDraft) {
          return;
        }

        if (!prompt.moveVisualUp(promptTextColumns)) {
          prompt.navigateUp();
        }

        return;
      }
      if (key.downArrow) {
        flushPendingSubmit();
        if (slashPreview.visible) {
          slashPreview.selectNext();
          return;
        }

        if (!hasPromptDraft) {
          return;
        }

        if (!prompt.moveVisualDown(promptTextColumns)) {
          prompt.navigateDown();
        }

        return;
      }
      if (key.leftArrow) {
        flushPendingSubmit();
        if (key.ctrl || key.meta) {
          prompt.moveWordLeft();
        } else {
          prompt.moveLeft();
        }

        return;
      }
      if (key.rightArrow) {
        flushPendingSubmit();
        if (key.ctrl || key.meta) {
          prompt.moveWordRight();
        } else {
          prompt.moveRight();
        }

        return;
      }
      if (key.home || (key.ctrl && input === "a")) {
        flushPendingSubmit();
        prompt.moveLineStart();
        return;
      }
      if (key.end || (key.ctrl && input === "e")) {
        flushPendingSubmit();
        prompt.moveLineEnd();
        return;
      }
      if (key.backspace) {
        flushPendingSubmit();
        if (key.ctrl || key.meta) {
          prompt.deleteWordBackward();
        } else {
          prompt.backspace();
        }

        return;
      }
      if (key.delete) {
        flushPendingSubmit();
        if (key.ctrl || key.meta) {
          prompt.deleteWordForward();
        } else {
          prompt.deleteForward();
        }

        return;
      }
      if (key.ctrl) {
        flushPendingSubmit();
        switch (input) {
          case "b":
            prompt.moveLeft();
            return;
          case "f":
            prompt.moveRight();
            return;
          case "g":
            onOpenTranscriptSearch();
            return;
          case "h":
            prompt.backspace();
            return;
          case "n":
            if (hasPromptDraft) {
              prompt.navigateDown();
            }
            return;
          case "o":
            prompt.insertNewline();
            return;
          case "p":
            if (hasPromptDraft) {
              prompt.navigateUp();
            }
            return;
          case "u":
            prompt.clear();
            return;
          case "w":
            prompt.deleteWordBackward();
            return;
          default:
            break;
        }
      }
      if (text) {
        ensurePendingSubmitDoesNotSwallowPaste(text);
        prompt.insertText(text);
        return;
      }
    },
    { isActive: !disabled },
  );

  usePaste((text) => {
    handleParsedPaste(text);
  });

  const showPlaceholder = prompt.value.length === 0;
  const promptMarker = mode === "bash" ? "! " : DEFAULT_PROMPT_MARKER;
  const renderedLines = useMemo(
    () =>
      renderInputLines(prompt.value, prompt.cursorOffset, promptTextColumns),
    [prompt.cursorOffset, prompt.value, promptTextColumns],
  );
  const promptStatusLabel = useMemo(
    () => formatPromptStatusLabel(statusLabel),
    [statusLabel],
  );
  const handleWheel = useCallback((event: { preventDefault(): void; stopPropagation(): void }) => {
    event.preventDefault();
    event.stopPropagation();
  }, []);

  return (
    <Box
      flexDirection="column"
      marginTop={1}
      userSelect="none"
      onWheel={handleWheel}
    >
      {isLoading ? (
        <Box paddingLeft={1} marginBottom={1}>
          <Box flexDirection="row" minWidth={0}>
            <Spinner type="arc" />
            <Box marginLeft={1}>
              <ShimmerText text={promptStatusLabel} />
            </Box>
          </Box>
        </Box>
      ) : null}
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor="$border"
        borderLeft={false}
        borderRight={false}
      >
        <Box flexDirection="column">
          {showPlaceholder ? (
            <Box>
              <Text color={mode === "bash" ? "$accent" : "$primary"} bold>
                {promptMarker}
              </Text>
              <Text color="$muted">
                Ask nami to inspect, plan, or edit code
              </Text>
              <Text color="$muted">{"█"}</Text>
            </Box>
          ) : (
            renderedLines.map((line, index) => (
              <Box key={index}>
                <Text
                  color={
                    index === 0
                      ? mode === "bash"
                        ? "$accent"
                        : "$primary"
                      : "$muted"
                  }
                  bold={index === 0}
                >
                  {index === 0 ? promptMarker : "  "}
                </Text>
                <Text>{line.length > 0 ? line : " "}</Text>
              </Box>
            ))
          )}
        </Box>
      </Box>
      {slashPreview.visible && slashPreview.matches.length > 0 && (
        <SlashCommandPreview
          commands={slashPreview.matches}
          selectedIndex={slashPreview.selectedIndex}
        />
      )}
    </Box>
  );
};

export default Input;
