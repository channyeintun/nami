import React, { type ComponentProps, type FC, type ReactNode } from "react";
import { Box, Text } from "ink";

interface MessageRowProps {
  children: ReactNode;
  label?: ReactNode;
  meta?: ReactNode;
  marker?: string;
  markerColor?: ComponentProps<typeof Text>["color"];
  markerDim?: boolean;
  marginBottom?: number;
}

const DEFAULT_MARKER = "●";

const MessageRow: FC<MessageRowProps> = ({
  children,
  label,
  meta,
  marker = DEFAULT_MARKER,
  markerColor,
  markerDim,
  marginBottom = 1,
}) => {
  return (
    <Box
      flexDirection="row"
      alignItems="flex-start"
      marginBottom={marginBottom}
    >
      <Box minWidth={2}>
        <Text color={markerColor} dimColor={markerDim}>
          {marker}
        </Text>
      </Box>
      <Box flexDirection="column" flexGrow={1}>
        {label || meta ? (
          <Box flexDirection="row" justifyContent="space-between">
            <Box flexGrow={1}>{label}</Box>
            {meta ? <Box marginLeft={1}>{meta}</Box> : null}
          </Box>
        ) : null}
        {children}
      </Box>
    </Box>
  );
};

export default MessageRow;
