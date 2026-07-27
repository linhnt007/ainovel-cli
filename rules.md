# Repository Rules

## Code Search and Exploration
- **ALWAYS** check for the presence of the `.codegraph/` directory at the repository root.
- If `.codegraph/` exists, **YOU MUST** use the CodeGraph MCP tool (`codegraph_explore`) or the CLI command (`codegraph explore "<query>"`) to search for, locate, or understand code symbols/functions **BEFORE** using `grep_search`, `find`, or directly viewing files.
- Refuse to use generic `grep_search` for locating code definitions if CodeGraph is active.
- **ALWAYS** using the CodeGraph MCP tool **BEFORE** any search related action. each search each use mcp
