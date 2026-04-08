import React, { type FC } from "react";
import { Box, Text, useInput } from "ink";

interface PermissionPromptProps {
  tool: string;
  command: string;
  risk: string;
  onRespond: (decision: "allow" | "deny" | "always_allow") => void;
}

const PermissionPrompt: FC<PermissionPromptProps> = ({ tool, command, risk, onRespond }) => {
  useInput((input) => {
    switch (input.toLowerCase()) {
      case "y":
        onRespond("allow");
        break;
      case "n":
        onRespond("deny");
        break;
      case "a":
        onRespond("always_allow");
        break;
    }
  });

  const riskColor = risk === "destructive" ? "red" : "yellow";

  return (
    <Box flexDirection="column" borderStyle="round" borderColor={riskColor} paddingX={1}>
      <Text bold color={riskColor}>
        Permission Required
      </Text>
      <Text>
        <Text bold>{tool}</Text>: {command}
      </Text>
      {risk && (
        <Text color={riskColor}>Risk: {risk}</Text>
      )}
      <Box marginTop={1}>
        <Text>
          <Text bold color="green">[y]</Text> Allow  
          <Text bold color="red">[n]</Text> Deny  
          <Text bold color="blue">[a]</Text> Always Allow
        </Text>
      </Box>
    </Box>
  );
};

export default PermissionPrompt;
