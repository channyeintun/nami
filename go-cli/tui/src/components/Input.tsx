import React, { useState, type FC } from "react";
import { Box, Text, useInput } from "ink";

interface InputProps {
  onSubmit: (text: string) => void;
  onModeToggle: () => void;
  onCancel: () => void;
  disabled?: boolean;
}

const Input: FC<InputProps> = ({ onSubmit, onModeToggle, onCancel, disabled }) => {
  const [value, setValue] = useState("");

  useInput((input, key) => {
    if (disabled) return;

    if (key.tab) {
      onModeToggle();
      return;
    }
    if (key.escape) {
      onCancel();
      return;
    }
    if (key.return) {
      if (value.trim()) {
        onSubmit(value.trim());
        setValue("");
      }
      return;
    }
    if (key.backspace || key.delete) {
      setValue((v) => v.slice(0, -1));
      return;
    }
    if (input) {
      setValue((v) => v + input);
    }
  });

  return (
    <Box>
      <Text color="cyan" bold>{"❯ "}</Text>
      <Text>{value}</Text>
      <Text color="gray">{"█"}</Text>
    </Box>
  );
};

export default Input;
