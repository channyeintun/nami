import React, { type FC, useState } from "react";
import { Box, Text, useInput } from "silvery";
import type { UIArtifactReview } from "../hooks/useEvents.js";

interface ArtifactReviewPromptProps {
  review: UIArtifactReview;
  onRespond: (
    decision: "approve" | "revise" | "cancel",
    feedback?: string,
  ) => void;
}

const ACTIONS = [
  { key: "a", label: "Approve", decision: "approve" as const, color: "green" },
  { key: "r", label: "Revise", decision: "revise" as const, color: "yellow" },
  { key: "c", label: "Cancel", decision: "cancel" as const, color: "gray" },
];

const ArtifactReviewPrompt: FC<ArtifactReviewPromptProps> = ({
  review,
  onRespond,
}) => {
  const [feedback, setFeedback] = useState("");
  const [reviseFeedbackMode, setReviseFeedbackMode] = useState(false);

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
        setFeedback((f) => f.slice(0, -1));
        return;
      }
      if (!key.ctrl && !key.meta && text) {
        setFeedback((f) => f + text);
      }
      return;
    }

    if (input === "a") {
      onRespond("approve");
    } else if (input === "r") {
      setReviseFeedbackMode(true);
    } else if (input === "c" || key.escape) {
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
      borderColor="blue"
      paddingX={1}
      marginTop={1}
    >
      <Box flexDirection="row" gap={1}>
        <Text bold color="blue">
          Review Plan:
        </Text>
        <Text>
          {review.title}
          {versionLabel}
        </Text>
      </Box>

      {reviseFeedbackMode ? (
        <Box flexDirection="column" marginTop={1}>
          <Text color="yellow">
            Revision notes (Enter to submit, Esc to cancel):
          </Text>
          <Box marginTop={0}>
            <Text color="yellow">{">"} </Text>
            <Text>{feedback}</Text>
            <Text color="gray">█</Text>
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
