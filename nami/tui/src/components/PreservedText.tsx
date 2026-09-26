import React, { type ComponentProps, type FC } from "react";
import { Box, Text } from "silvery";
import { expandTabs } from "../utils/text.js";

interface PreservedTextProps {
  text: string;
  color?: ComponentProps<typeof Text>["color"];
  bold?: boolean;
}

const PreservedText: FC<PreservedTextProps> = ({
  text,
  color,
  bold,
}) => {
  const lines = expandTabs(text.replace(/\r\n/g, "\n")).split("\n");

  return (
    <Box flexDirection="column" width="100%" minWidth={0}>
      {lines.map((line, index) => (
        <Text
          key={`line-${index}`}
          color={color}
          bold={bold}
          wrap="wrap"
        >
          {line.length > 0 ? line : " "}
        </Text>
      ))}
    </Box>
  );
};

export default PreservedText;
