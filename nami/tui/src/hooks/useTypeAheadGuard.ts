import { useCallback, useState } from "react";

/** How long a prompt must be on screen before a key can answer it. */
export const TYPE_AHEAD_GUARD_MS = 400;

/**
 * Keys typed ahead must not answer a prompt. The user is encouraged to write a
 * follow-up while the agent works, and when a prompt appears mid-word the
 * next keystroke would land on it - where a single letter or Enter approves.
 * The returned function reports whether the prompt has been on screen long
 * enough for a key to count as a decision. The clock starts when the prompt
 * mounts, so render each request as its own prompt (key it by request id).
 */
export function useTypeAheadGuard(): () => boolean {
  const [shownAt] = useState(() => Date.now());
  return useCallback(
    () => Date.now() - shownAt >= TYPE_AHEAD_GUARD_MS,
    [shownAt],
  );
}
