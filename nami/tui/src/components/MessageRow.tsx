import React, { type ComponentProps, type FC, type ReactNode } from "react";
import { Box, Text } from "silvery";

interface MessageRowProps {
  children: ReactNode;
  label?: ReactNode;
  meta?: ReactNode;
  marker?: ReactNode;
  markerColor?: ComponentProps<typeof Text>["color"];
  markerDim?: boolean;
  marginBottom?: number;
}

const DEFAULT_MARKER: ReactNode = "●";

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
      minWidth={0}
    >
      <Box minWidth={2}>
        {typeof marker === "string" ? (
          <Text color={markerDim ? "$muted" : markerColor}>
            {marker}
          </Text>
        ) : (
          marker
        )}
      </Box>
      <Box flexDirection="column" flexGrow={1} minWidth={0}>
        {label || meta ? (
          <Box flexDirection="row" justifyContent="space-between" minWidth={0}>
            <Box flexGrow={1} minWidth={0}>{label}</Box>
            {meta ? <Box marginLeft={1}>{meta}</Box> : null}
          </Box>
        ) : null}
        {children}
      </Box>
    </Box>
  );
};

export default MessageRow;
