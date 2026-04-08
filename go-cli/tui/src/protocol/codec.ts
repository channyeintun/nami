import type { StreamEvent, ClientMessage } from "./types.js";

/**
 * Parse a single NDJSON line into a StreamEvent.
 */
export function parseEvent(line: string): StreamEvent | null {
  const trimmed = line.trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed) as StreamEvent;
  } catch {
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
