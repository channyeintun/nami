import React, { type FC, useState } from "react";
import { Box, Text, useInput } from "silvery";
import { withoutLastCharacter } from "../utils/text.js";
import type { UIArtifactReview } from "../hooks/useEvents.js";
import { useTypeAheadGuard } from "../hooks/useTypeAheadGuard.js";

interface ArtifactReviewPromptProps {
  review: UIArtifactReview;
  onRespond: (
    decision: "approve" | "revise" | "cancel",
    feedback?: string,
  ) => void;
}

const ACTIONS = [
  {
    key: "a",
    label: "Approve",
    decision: "approve" as const,
    color: "$success",
  },
  { key: "r", label: "Revise", decision: "revise" as const, color: "$warning" },
  { key: "c", label: "Cancel", decision: "cancel" as const, color: "$muted" },
];

const ArtifactReviewPrompt: FC<ArtifactReviewPromptProps> = ({
  review,
  onRespond,
}) => {
  const [feedback, setFeedback] = useState("");
  const [reviseFeedbackMode, setReviseFeedbackMode] = useState(false);
  // "a" approves the plan and lets the agent start writing, so a letter typed
  // for the prompt behind this one must not decide it.
  const canDecide = useTypeAheadGuard();

  useInput((input, key) => {
    const text = key.text ?? input;

    if (reviseFeedbackMode) {
      if (key.return) {
        onRespond("revise", feedback.trim() || undefined);
        return;
      }
      if (key.escape) {
        setReviseFeedbackMode(false);
        setFeedback("");
        return;
      }
      if (key.backspace || key.delete) {
        setFeedback(withoutLastCharacter);
        return;
      }
      if (!key.ctrl && !key.meta && text) {
        setFeedback((f) => f + text);
      }
      return;
    }

    if (key.escape) {
      onRespond("cancel");
      return;
    }
    if (!canDecide()) {
      return;
    }
    if (input === "a") {
      onRespond("approve");
    } else if (input === "r") {
      setReviseFeedbackMode(true);
    } else if (input === "c") {
      onRespond("cancel");
    }
  });

  const versionLabel = review.version > 0 ? ` v${review.version}` : "";

  return (
    <Box
      flexDirection="column"
      flexGrow={0}
      flexShrink={1}
      minHeight={0}
      borderStyle="round"
      borderColor="$border"
      paddingX={1}
      marginTop={1}
      userSelect="contain"
    >
      <Box flexDirection="row" gap={1}>
        <Text bold color="$primary">
          Review Plan:
        </Text>
        <Text>
          {review.title}
          {versionLabel}
        </Text>
      </Box>

      {reviseFeedbackMode ? (
        <Box flexDirection="column" marginTop={1}>
          <Text color="$warning">
            Revision notes (Enter to submit, Esc to cancel):
          </Text>
          <Box marginTop={0}>
            <Text color="$warning">{">"} </Text>
            <Text>{feedback}</Text>
            <Text color="$muted">█</Text>
          </Box>
        </Box>
      ) : (
        <Box flexDirection="row" gap={3} marginTop={1}>
          {ACTIONS.map(({ key, label, color }) => (
            <Text key={key} color={color}>
              [{key}] {label}
            </Text>
          ))}
        </Box>
      )}
    </Box>
  );
};

export default ArtifactReviewPrompt;
