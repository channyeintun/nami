package compact

// CompactionPromptTemplate is the 9-section summary format adapted from
// the production-tested prompt in services/compact/prompt.ts.
const CompactionPromptTemplate = `Summarize the following conversation for an AI coding assistant. Preserve ALL of the following:

1. **Primary Request**: What the user originally asked for
2. **Technical Concepts**: Languages, frameworks, APIs, patterns discussed
3. **Files & Code**: All file paths mentioned, code snippets written or discussed, modifications made
4. **Errors & Fixes**: Any errors encountered, debugging steps taken, solutions found
5. **Problem Solving**: Key decisions, trade-offs discussed, approaches tried
6. **All User Messages**: Preserve the intent and specifics of every user message
7. **Pending Tasks**: Anything not yet completed, open questions
8. **Current Work**: What was being worked on when this summary was requested
9. **Optional Next Step**: If there's a clear next action, state it

Format as a structured summary that another AI can use to continue the conversation seamlessly.
Do NOT include tool call details or raw API responses — only their meaningful outcomes.
Keep the summary concise but complete. Aim for 1000-2000 tokens.`
