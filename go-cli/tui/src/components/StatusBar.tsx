import React, { type FC } from "react";
import { Box, Text } from "ink";

interface StatusBarProps {
  mode: string;
  model: string;
  totalCostUsd: number;
  inputTokens: number;
  outputTokens: number;
}

const StatusBar: FC<StatusBarProps> = ({ mode, model, totalCostUsd, inputTokens, outputTokens }) => {
  const modeColor = mode === "plan" ? "blue" : "green";

  return (
    <Box borderStyle="single" borderColor="gray" paddingX={1} justifyContent="space-between">
      <Text>
        <Text color={modeColor} bold>{`[${mode.toUpperCase()}]`}</Text>
        {"  "}
        <Text color="yellow">{model}</Text>
      </Text>
      <Text color="gray">
        {`$${totalCostUsd.toFixed(4)} | ${inputTokens}↑ ${outputTokens}↓`}
      </Text>
    </Box>
  );
};

export default StatusBar;
