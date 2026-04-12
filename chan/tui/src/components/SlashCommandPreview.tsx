import React, { type FC } from "react";
import { Box, Text } from "ink";
import type { UISlashCommand } from "../hooks/useEvents.js";

interface SlashCommandPreviewProps {
  commands: UISlashCommand[];
  selectedIndex: number;
}

const SlashCommandPreview: FC<SlashCommandPreviewProps> = ({
  commands,
  selectedIndex,
}) => {
  const startIndex = Math.max(0, Math.min(selectedIndex, commands.length - 6));
  const visibleCommands = commands.slice(startIndex, startIndex + 6);

  return (
    <Box flexDirection="column" marginTop={1}>
      {visibleCommands.map((command, index) => {
        const actualIndex = startIndex + index;
        const selected = actualIndex === selectedIndex;

        return (
          <Box key={command.name} paddingLeft={1}>
            <Text color={selected ? "cyan" : "gray"}>
              {selected ? "›" : " "}
            </Text>
            <Text color={selected ? "cyan" : "white"} bold>
              {` /${command.name}`}
            </Text>
            <Text color="gray">{`  ${command.description}`}</Text>
          </Box>
        );
      })}
    </Box>
  );
};

export default SlashCommandPreview;
