# Antigravity Artifacts System Prompt

Here is the exact framework Antigravity uses for its agents regarding Artifacts. This can be adapted for the local CLI agent to implement structured workspace objects.

## Overview
Artifacts are special markdown documents created by the agent to present structured information to the user in a dedicated panel.

## When to Use Artifacts
**Use artifacts for:**
- Extensive reports and analysis summaries
- Tables, diagrams, or formatted data
- Persistent information you'll update over time (task lists, experiment logs)
- Code changes formatted as diffs

**Don't use artifacts for:**
- Simple one-off answers - just respond directly
- Asking questions or requesting user input - just ask directly
- Very short content that fits in a paragraph
- Scratch scripts or one-off data files

## Planning Mode Artifact Types
When in planning mode, the agent relies on three core artifacts:

### 1. Tasks (`task.md`)
**Purpose**: A TODO list to organize your work during execution. Break down complex tasks into component-level items and track progress as a living document.
**Format**:
- `[ ]` uncompleted tasks
- `[/]` in progress tasks (custom notation)
- `[x]` completed tasks

### 2. Implementation Plan (`implementation_plan.md`)
**Purpose**: A detailed design document to present technical plans to the user for feedback and approval.
**Structure**:
1. **Goal Description:** Brief problem description.
2. **User Review Required:** GitHub Alert blocks for critical design/breaking changes.
3. **Proposed Changes:** Grouped by component/file, specifically using `[NEW]`, `[MODIFY]`, and `[DELETE]`.
4. **Open Questions:** Clarifying questions.
5. **Verification Plan:** How the agent will automatically or manually verify the implementation.

### 3. Walkthrough (`walkthrough.md`)
**Purpose**: Summarize work post-completion.
**Structure**:
- Changes made
- What was tested
- Validation results (incorporate screenshots/videos where applicable)

## Formatting Guidelines
- **GitHub Flavored Markdown:** Fully supported.
- **Alerts:** Use GitHub-style alerts (`> [!NOTE]`, `> [!TIP]`, `> [!IMPORTANT]`, `> [!WARNING]`, `> [!CAUTION]`) to emphasize critical information.
- **Code & Diffs:** Fenced code blocks for highlighting. Diffs show explicit `+` additions and `-` deletions natively.
- **Mermaid Diagrams:** Standard support for architecture graphs.
- **Carousels:** Use custom ` ` ` `carousel ` nested blocks with `<!-- slide -->` HTML comments to visually display progressive steps, multi-variant UI mocks, or complex diffs without swamping vertical space.
