import React, { type FC } from "react";
import { Box, Text } from "ink";

interface StreamOutputProps {
  text: string;
}

const StreamOutput: FC<StreamOutputProps> = ({ text }) => {
  if (!text) return null;

  return (
    <Box flexDirection="column" paddingLeft={1}>
      <Text>{text}</Text>
    </Box>
  );
};

export default StreamOutput;
