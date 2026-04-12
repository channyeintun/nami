import React, { type FC, useMemo, useRef } from "react";
import { Box, Text } from "ink";
import MarkdownTable from "./MarkdownTable.js";
import { renderMarkdownBlocks, cachedLexer } from "../utils/markdown.js";

interface MarkdownTextProps {
  text: string;
  streaming?: boolean;
}

const MarkdownTextBody: FC<Pick<MarkdownTextProps, "text">> = ({ text }) => {
  const rendered = useMemo(() => {
    return renderMarkdownBlocks(text);
  }, [text]);

  return (
    <Box flexDirection="column">
      {rendered.map((block, index) => {
        if (block.kind === "table") {
          return <MarkdownTable key={`table-${index}`} token={block.token} />;
        }

        return <Text key={`text-${index}`}>{block.content}</Text>;
      })}
    </Box>
  );
};

const StreamingMarkdownText: FC<Pick<MarkdownTextProps, "text">> = ({
  text,
}) => {
  const stablePrefixRef = useRef("");
  const normalized = text.replace(/\r\n/g, "\n");

  if (!normalized.startsWith(stablePrefixRef.current)) {
    stablePrefixRef.current = "";
  }

  const boundary = stablePrefixRef.current.length;
  const tokens = cachedLexer(normalized.slice(boundary));
  let lastContentIndex = tokens.length - 1;

  while (lastContentIndex >= 0 && tokens[lastContentIndex]?.type === "space") {
    lastContentIndex -= 1;
  }

  let advance = 0;
  for (let index = 0; index < lastContentIndex; index += 1) {
    advance += tokens[index]?.raw.length ?? 0;
  }

  if (advance > 0) {
    stablePrefixRef.current = normalized.slice(0, boundary + advance);
  }

  const stablePrefix = stablePrefixRef.current;
  const unstableSuffix = normalized.slice(stablePrefix.length);

  return (
    <Box flexDirection="column">
      {stablePrefix ? <MarkdownTextBody text={stablePrefix} /> : null}
      {unstableSuffix ? <MarkdownTextBody text={unstableSuffix} /> : null}
    </Box>
  );
};

const MarkdownText: FC<MarkdownTextProps> = ({ text, streaming }) => {
  if (streaming) {
    return <StreamingMarkdownText text={text} />;
  }

  return <MarkdownTextBody text={text} />;
};

export default MarkdownText;
