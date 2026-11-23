# Zipdeck VSCodium extension

Requires the `zipdeck` Go executable installed on PATH, or set `zipdeck.executable` in VSCodium settings.

## Commands

- **Zipdeck: Open TUI** — opens the terminal UI in the current workspace.
- **Zipdeck: Zip Selected Files / Folders** — right click selection in Explorer; creates a ZIP. Directory contents recurse while skipping ignored child directories and files.
- **Zipdeck: Zip Open Editor Files** — archives the local text files currently open in editor tabs.
- **Zipdeck: Send Selected Files to LLM** — right click Explorer items, enter mandatory instructions, receive a new ZIP. Uses existing `~/.config/zipdeck/config.json`.
- **Zipdeck: Ask LLM About Selection** — highlight code, right click, enter prompt; the model's answer opens in a separate, unsaved Markdown editor. Does not modify source files.

Default output folder is the parent of your workspace. Settings: `zipdeck.executable`, `zipdeck.outputDirectory`, `zipdeck.snippetSystemPrompt`.

Snippet mode supports OpenAI-compatible chat completions. It reads the `api_key_env` variable from the VSCodium process environment, which may differ from a terminal shell's environment. Multipart ZIP mode is only supported for the file submission command, not the snippet command.

Security: explicit selections override exclusions. Avoid selecting `.env` or credential files; source is sent to the configured external endpoint. ZIP output is not automatically extracted or applied. The extension currently works with local single-root workspaces for each action and does not package unsaved editor contents.

## Build it

`npm install -g @vscode/vsce`

`cd extension`

`vsce package`

