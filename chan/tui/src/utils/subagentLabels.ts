export function formatSubagentType(subagentType: string): string {
  switch (subagentType) {
    case "search":
      return "Search";
    case "execution":
      return "Execution";
    case "general-purpose":
      return "General Purpose";
    case "explore":
      return "Explore";
    default:
      return subagentType;
  }
}
