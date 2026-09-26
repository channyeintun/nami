import React, { type FC, useEffect, useMemo, useState } from "react";
import { Box, ModalDialog, Text, useInput } from "silvery";
import { withoutLastCharacter } from "../utils/text.js";
import type {
  UIAskUserQuestionAnswer,
  UIAskUserQuestionRequest,
} from "../hooks/useEvents.js";

type Question = UIAskUserQuestionRequest["questions"][number];

const CONTROL_CHARACTER = /[\u0000-\u001f\u007f]/;

interface AskUserQuestionPromptProps {
  request: UIAskUserQuestionRequest;
  onSubmit: (
    status: "answered" | "declined" | "cancelled",
    answers: UIAskUserQuestionAnswer[],
  ) => void;
}

const AskUserQuestionPrompt: FC<AskUserQuestionPromptProps> = ({
  request,
  onSubmit,
}) => {
  const initialAnswers = useMemo(
    () =>
      request.questions.map((question) => {
        const recommended = question.options.find(
          (option) => option.recommended,
        );
        const initialValue = recommended?.value ?? question.options[0]?.value;
        return {
          header: question.header,
          selectedValues: initialValue ? [initialValue] : [],
          freeformText: "",
          rawAnswer: initialValue ?? "",
        };
      }),
    [request.questions],
  );
  const [questionIndex, setQuestionIndex] = useState(0);
  const [answers, setAnswers] =
    useState<UIAskUserQuestionAnswer[]>(initialAnswers);
  const [optionIndex, setOptionIndex] = useState(0);
  const [freeformDraft, setFreeformDraft] = useState("");
  const [terminalRows, setTerminalRows] = useState(process.stdout.rows ?? 24);
  const [terminalColumns, setTerminalColumns] = useState(
    process.stdout.columns ?? 80,
  );

  useEffect(() => {
    setQuestionIndex(0);
    setAnswers(initialAnswers);
    setOptionIndex(0);
    setFreeformDraft(initialAnswers[0]?.freeformText ?? "");
  }, [initialAnswers, request.requestId]);

  useEffect(() => {
    const handleResize = () => {
      setTerminalRows(process.stdout.rows ?? 24);
      setTerminalColumns(process.stdout.columns ?? 80);
    };

    handleResize();
    process.stdout.on("resize", handleResize);

    return () => {
      process.stdout.off("resize", handleResize);
    };
  }, []);

  const currentQuestion = request.questions[questionIndex];
  const currentAnswer = answers[questionIndex] ?? initialAnswers[questionIndex];

  const persistCurrentAnswer = (question: Question) => {
    const freeformText = question.allowFreeform ? freeformDraft.trim() : "";
    setAnswers((existing) =>
      existing.map((answer, index) => {
        if (index !== questionIndex) {
          return answer;
        }
        const rawAnswer = [...answer.selectedValues, freeformText]
          .filter((value) => value.length > 0)
          .join(", ");
        return {
          ...answer,
          freeformText,
          rawAnswer,
        };
      }),
    );
  };

  const buildFinalAnswers = (question: Question) =>
    answers.map((answer, index) => {
      if (index !== questionIndex) {
        return answer;
      }
      const freeformText = question.allowFreeform
        ? freeformDraft.trim()
        : answer.freeformText;
      const rawAnswer = [...answer.selectedValues, freeformText]
        .filter((value) => value.length > 0)
        .join(", ");
      return {
        ...answer,
        freeformText,
        rawAnswer,
      };
    });

  const toggleSelection = (question: Question) => {
    const option = question.options[optionIndex];
    if (!option) {
      return;
    }
    setAnswers((existing) =>
      existing.map((answer, index) => {
        if (index !== questionIndex) {
          return answer;
        }
        if (question.multiSelect) {
          const selectedValues = answer.selectedValues.includes(option.value)
            ? answer.selectedValues.filter((value) => value !== option.value)
            : [...answer.selectedValues, option.value];
          return {
            ...answer,
            selectedValues,
          };
        }
        return {
          ...answer,
          selectedValues: [option.value],
        };
      }),
    );
  };

  // Registered before the early return below, so the hook order does not
  // depend on whether there is a question to show.
  useInput((input, key) => {
    if (key.escape) {
      onSubmit("cancelled", []);
      return;
    }
    if (key.ctrl && input === "d") {
      onSubmit("declined", []);
      return;
    }
    if (!currentQuestion) {
      return;
    }

    // Each key event carries one grapheme, which can be several UTF-16
    // units: an emoji, a flag, a letter with a combining accent.
    const text = key.text ?? input;
    if (
      currentQuestion.allowFreeform &&
      typeof text === "string" &&
      text.length > 0 &&
      !CONTROL_CHARACTER.test(text) &&
      !key.ctrl &&
      !key.meta &&
      !key.return &&
      !key.upArrow &&
      !key.downArrow
    ) {
      setFreeformDraft((value) => value + text);
      return;
    }
    if (currentQuestion.allowFreeform && key.backspace) {
      setFreeformDraft(withoutLastCharacter);
      return;
    }

    if (key.upArrow) {
      if (currentQuestion.options.length > 0) {
        setOptionIndex((index) =>
          index <= 0 ? currentQuestion.options.length - 1 : index - 1,
        );
      }
      return;
    }
    if (key.downArrow) {
      if (currentQuestion.options.length > 0) {
        setOptionIndex((index) => (index + 1) % currentQuestion.options.length);
      }
      return;
    }
    if (input === " " && currentQuestion.options.length > 0) {
      toggleSelection(currentQuestion);
      return;
    }
    if (!currentQuestion.multiSelect && currentQuestion.options.length > 0) {
      const shortcut = input?.toLowerCase() ?? "";
      const numericIndex = Number.parseInt(shortcut, 10);
      if (
        !Number.isNaN(numericIndex) &&
        numericIndex >= 1 &&
        numericIndex <= currentQuestion.options.length
      ) {
        setOptionIndex(numericIndex - 1);
        setAnswers((existing) =>
          existing.map((answer, index) =>
            index === questionIndex
              ? {
                  ...answer,
                  selectedValues: [
                    currentQuestion.options[numericIndex - 1].value,
                  ],
                }
              : answer,
          ),
        );
        return;
      }
    }
    if (key.return) {
      persistCurrentAnswer(currentQuestion);
      if (questionIndex >= request.questions.length - 1) {
        onSubmit("answered", buildFinalAnswers(currentQuestion));
        return;
      }
      const nextIndex = questionIndex + 1;
      setQuestionIndex(nextIndex);
      setOptionIndex(0);
      setFreeformDraft(
        answers[nextIndex]?.freeformText ??
          initialAnswers[nextIndex]?.freeformText ??
          "",
      );
    }
  });

  if (!currentQuestion || !currentAnswer) {
    return null;
  }

  const dialogWidth =
    terminalColumns > 52
      ? Math.min(96, terminalColumns - 4)
      : Math.max(24, terminalColumns - 2);
  const dialogHeight =
    terminalRows > 16
      ? Math.min(24, terminalRows - 4)
      : Math.max(10, terminalRows - 2);

  return (
    <ModalDialog
      title="Clarification Required"
      width={dialogWidth}
      height={dialogHeight}
      borderStyle="single"
      borderColor="$inputborder"
      footer="Enter next/submit · Up/Down move · Space toggle · Ctrl+D decline · Esc cancel"
    >
      <Box
        flexDirection="column"
        flexGrow={1}
        flexShrink={1}
        minWidth={0}
        minHeight={0}
      >
        <Box flexDirection="column" flexShrink={0} minWidth={0}>
          <Text>{`Question ${questionIndex + 1} of ${request.questions.length}`}</Text>
          <Text bold>{currentQuestion.question}</Text>
          <Text color="$muted">Header: {currentQuestion.header}</Text>
        </Box>

        {currentQuestion.options.length > 0 ? (
          <Box
            marginTop={1}
            flexDirection="column"
            minWidth={0}
            flexGrow={1}
            flexShrink={1}
            minHeight={0}
            overflow="scroll"
          >
            {currentQuestion.options.map((option, index) => {
              const selected = currentAnswer.selectedValues.includes(
                option.value,
              );
              const cursor = index === optionIndex;
              return (
                <Box key={option.value} flexDirection="column" marginBottom={1}>
                  <Text color={cursor ? "$primary" : "$fg"} bold={cursor}>
                    {cursor ? "›" : " "} {selected ? "[x]" : "[ ]"} {index + 1}.{" "}
                    {option.label}
                  </Text>
                  {option.description ? (
                    <Text color="$muted"> {option.description}</Text>
                  ) : null}
                </Box>
              );
            })}
          </Box>
        ) : null}

        {currentQuestion.allowFreeform ? (
          <Box marginTop={1} flexDirection="column" minWidth={0} flexShrink={0}>
            <Text color="$muted">Optional note</Text>
            <Text>{freeformDraft || "Type to add freeform context..."}</Text>
          </Box>
        ) : null}
      </Box>
    </ModalDialog>
  );
};

export default AskUserQuestionPrompt;
