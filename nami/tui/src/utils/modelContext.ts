export function inferContextWindow(model: string): number {
  const normalized = model.toLowerCase();

  if (normalized.includes("claude")) {
    return 200_000;
  }
  if (normalized.includes("gemini")) {
    return 1_000_000;
  }
  if (normalized.includes("deepseek")) {
    return 64_000;
  }
  if (normalized.includes("qwen") || normalized.includes("llama-4")) {
    return 131_072;
  }
  if (normalized.includes("glm") || normalized.includes("mistral")) {
    return 128_000;
  }
  if (normalized.includes("gemma") || normalized.includes("ollama")) {
    return 32_000;
  }
  if (
    normalized.includes("gpt") ||
    normalized.includes("o1") ||
    normalized.includes("o3") ||
    normalized.includes("o4")
  ) {
    return 128_000;
  }

  return 128_000;
}

export function formatTokenCount(value: number): string {
  if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(1)}M`;
  }
  if (value >= 1_000) {
    return `${(value / 1_000).toFixed(1)}k`;
  }
  return `${value}`;
}
