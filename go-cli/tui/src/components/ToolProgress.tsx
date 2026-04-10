import React, { type FC } from "react";
import { Box, Text } from "ink";
import Spinner from "ink-spinner";

interface ToolProgressProps {
  toolName: string;
}

const ToolProgress: FC<ToolProgressProps> = ({ toolName }) => {
  return (
    <Box paddingLeft={1}>
      <Text color="gray">
        <Spinner type="dots" /> {"Tool: "}
        <Text bold>{toolName}</Text>
        {" running"}
      </Text>
    </Box>
  );
};

export default ToolProgress;
