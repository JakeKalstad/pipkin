# Zipdeck

A tiny, dependency-free Go terminal application to select project files, zip them, and optionally request LLM edits that arrive as a **separate** local ZIP. Designed for Linux/macOS terminal sessions (uses `stty`).

## Install

```bash
go build -o zipdeck ./cmd/zipdeck
sudo install -m 755 zipdeck /usr/local/bin/zipdeck
```

Run `zipdeck` in the project directory, or `zipdeck /path/to/project`.

## Controls

| Key | Action |
|---|---|
| Up/Down or j/k | Navigate |
| Right/Left or l/h | Expand/collapse directory |
| Space | Check/uncheck file or entire directory |
| a / n | Select all / select none |
| z | Write `<project>.zip` beside the project |
| p or Shift+L | Enter a prompt and submit to configured LLM |
| Enter | Submit prompt |
| Ctrl+G | Cancel prompt |
| q | Quit |

Ignored build/dependency directories are initially unchecked and not traversed until expanded or toggled (for speed). Common secret/environment files are also unchecked. **Review selections before submission**: this is not a security scanner; unusual credentials or sensitive files may remain selected.

## Configure your LLM

Create `~/.config/zipdeck/config.json`.

### OpenAI-compatible chat completions (default)

```json
{
  "provider": "openai",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-4.1-mini",
  "api_key_env": "OPENAI_API_KEY",
  "max_context_bytes": 160000
}
```

Then `export OPENAI_API_KEY='...'` before running. Other OpenAI-compatible servers may work if they expose `/chat/completions` and return JSON content. Configure your own base URL, model, and key environment variable.

**Important:** OpenAI-compatible chat completions **do not receive the binary ZIP**. Zipdeck sends the prompt and the contents of selected text files that fit its context budget. The model responds with JSON replacements/deletions, and Zipdeck packages them with the unchanged selected files into `<project>-llm.zip`. Files larger than 50 KB, binary files, and text files beyond the configured budget remain in the resulting ZIP unchanged. The model can edit only submitted text files. No code is executed, and the original source tree is untouched.

### Multipart ZIP gateway (literal ZIP upload/download)

```json
{
  "provider": "multipart",
  "endpoint": "https://your-llm-gateway.example/api/edit-zip",
  "model": "your-model",
  "api_key_env": "ZIPDECK_API_KEY"
}
```

This sends `multipart/form-data` fields `file` (binary ZIP), `prompt` (required), and `model`, optionally using `Authorization: Bearer $ZIPDECK_API_KEY`. The gateway **must return a ZIP body** on a 2xx response. Ordinary LLM APIs do not implement this protocol, so you need a gateway that does.

## Output

- `<project>.zip` — selected files
- `<project>-llm.zip` — result from the model or multipart gateway

Zipdeck never extracts a response into your project or automatically overwrites your source. Inspect or diff the returned ZIP before using it. The ZIP gateway response is structurally validated (including rejecting path traversal entries) but its contents are untrusted.

## Limitations

Minimal MVP; not a full file manager. No mouse support, long-running calls block interaction, no in-app diff, no streaming, and response changes may fail if the model doesn't obey the JSON contract. Includes no secret detector or explicit token/cost estimate. The project may have files that are too large for the context window.

## Test

```bash
go test ./...
```
