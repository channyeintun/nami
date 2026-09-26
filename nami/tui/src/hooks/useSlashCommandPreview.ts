import { useEffect, useMemo, useState } from "react";
import type { UISlashCommand } from "./useEvents.js";

interface UseSlashCommandPreviewOptions {
  value: string;
  cursorOffset: number;
  slashCommands: UISlashCommand[];
}

interface SlashCommandPreviewState {
  visible: boolean;
  query: string;
  matches: UISlashCommand[];
  selectedIndex: number;
  selectedCommand: UISlashCommand | null;
  selectNext: () => void;
  selectPrevious: () => void;
  applySelection: () => string | null;
}

export function useSlashCommandPreview({
  value,
  cursorOffset,
  slashCommands,
}: UseSlashCommandPreviewOptions): SlashCommandPreviewState {
  const slashToken = useMemo(
    () => getSlashCommandToken(value, cursorOffset),
    [cursorOffset, value],
  );
  const matches = useMemo(
    () => rankSlashCommands(slashCommands, slashToken.query),
    [slashCommands, slashToken.query],
  );
  const [selectedIndex, setSelectedIndex] = useState(0);

  useEffect(() => {
    setSelectedIndex(0);
  }, [slashToken.query]);

  useEffect(() => {
    setSelectedIndex((current) => {
      if (matches.length === 0) {
        return 0;
      }

      return Math.min(current, matches.length - 1);
    });
  }, [matches]);

  const visible = slashToken.visible && matches.length > 0;
  const selectedCommand = visible ? (matches[selectedIndex] ?? null) : null;

  return {
    visible,
    query: slashToken.query,
    matches,
    selectedIndex,
    selectedCommand,
    selectNext: () => {
      if (matches.length === 0) {
        return;
      }

      setSelectedIndex((current) => (current + 1) % matches.length);
    },
    selectPrevious: () => {
      if (matches.length === 0) {
        return;
      }

      setSelectedIndex(
        (current) => (current - 1 + matches.length) % matches.length,
      );
    },
    applySelection: () => {
      if (!selectedCommand) {
        return null;
      }

      return selectedCommand.takesArguments
        ? `/${selectedCommand.name} `
        : `/${selectedCommand.name}`;
    },
  };
}

function getSlashCommandToken(value: string, cursorOffset: number) {
  if (!value.startsWith("/") || value.length === 0) {
    return { visible: false, query: "" };
  }

  if (value !== value.trimEnd()) {
    return { visible: false, query: "" };
  }

  const firstWhitespace = value.search(/\s/);
  if (firstWhitespace !== -1) {
    return { visible: false, query: "" };
  }

  if (cursorOffset > value.length) {
    return { visible: false, query: "" };
  }

  return {
    visible: true,
    query: value.slice(1).toLowerCase(),
  };
}

function rankSlashCommands(
  slashCommands: UISlashCommand[],
  query: string,
): UISlashCommand[] {
  const normalizedQuery = query.trim().toLowerCase();
  const ranked = slashCommands
    .map((command, index) => ({
      command,
      index,
      name: command.name.toLowerCase(),
      description: command.description.toLowerCase(),
      nameIndex:
        normalizedQuery.length > 0
          ? command.name.toLowerCase().indexOf(normalizedQuery)
          : 0,
      descriptionWordIndex:
        normalizedQuery.length > 0
          ? firstDescriptionWordMatch(command.description, normalizedQuery)
          : 0,
    }))
    .filter(({ nameIndex, descriptionWordIndex }) => {
      if (normalizedQuery.length === 0) {
        return true;
      }

      return nameIndex !== -1 || descriptionWordIndex !== -1;
    })
    .sort((left, right) => {
      // A fully typed name is the command the user means, even when a longer
      // name it prefixes comes earlier in the catalog: Enter runs the
      // selected command.
      const leftExact = left.name === normalizedQuery ? 0 : 1;
      const rightExact = right.name === normalizedQuery ? 0 : 1;
      if (leftExact !== rightExact) {
        return leftExact - rightExact;
      }

      const leftStartsWith = left.name.startsWith(normalizedQuery) ? 0 : 1;
      const rightStartsWith = right.name.startsWith(normalizedQuery) ? 0 : 1;
      if (leftStartsWith !== rightStartsWith) {
        return leftStartsWith - rightStartsWith;
      }

      const leftNameMatchRank = left.nameIndex === -1 ? 1 : 0;
      const rightNameMatchRank = right.nameIndex === -1 ? 1 : 0;
      if (leftNameMatchRank !== rightNameMatchRank) {
        return leftNameMatchRank - rightNameMatchRank;
      }

      if (left.nameIndex !== right.nameIndex) {
        return left.nameIndex - right.nameIndex;
      }

      const leftDescriptionMatchRank = left.descriptionWordIndex === -1 ? 1 : 0;
      const rightDescriptionMatchRank =
        right.descriptionWordIndex === -1 ? 1 : 0;
      if (leftDescriptionMatchRank !== rightDescriptionMatchRank) {
        return leftDescriptionMatchRank - rightDescriptionMatchRank;
      }

      if (left.descriptionWordIndex !== right.descriptionWordIndex) {
        return left.descriptionWordIndex - right.descriptionWordIndex;
      }

      return left.index - right.index;
    });

  return ranked.map(({ command }) => command);
}

function firstDescriptionWordMatch(description: string, query: string): number {
  const tokens = description
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((token) => token.length > 0);

  for (let index = 0; index < tokens.length; index += 1) {
    if (tokens[index]?.startsWith(query)) {
      return index;
    }
  }

  return -1;
}
