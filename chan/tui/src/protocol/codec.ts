import type { StreamEvent, ClientMessage } from "./types.js";

/**
 * Parse a single NDJSON line into a StreamEvent.
 */
export function parseEvent(line: string): StreamEvent | null {
  const trimmed = line.trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed) as StreamEvent;
  } catch (error) {
    const preview =
      trimmed.length > 400 ? `${trimmed.slice(0, 400)}...` : trimmed;
    console.error("[gocode:tui] Failed to parse engine event JSON", {
      error,
      preview,
    });
    return null;
  }
}

/**
 * Serialize a ClientMessage to an NDJSON line.
 */
export function serializeMessage(msg: ClientMessage): string {
  return JSON.stringify(msg) + "\n";
}

/**
 * Create a typed client message.
 */
export function createMessage(
  type: ClientMessage["type"],
  payload?: unknown
): ClientMessage {
  return payload !== undefined ? { type, payload } : { type };
}
