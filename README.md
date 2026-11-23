# Zipdeck

**Pick your files. ZIP them up. Let an LLM do its thing. Review the diff.**

A lightweight Go CLI/TUI and VSCodium extension for making targeted code changes with LLMs, without handing an agent your entire repository.

## Why?

I use tools like OpenCode constantly, but not every task needs a full-blown coding agent.

Sometimes I know exactly which files need changing. I just want to ZIP them up, send them to an LLM with a prompt, get the updated files back, and review the changes with Git.

That workflow works great. Doing it manually a dozen times a day doesn't.

**Enter Zipdeck.**

## How it works

```text
Select files → ZIP → Prompt → LLM → Updated ZIP → Diff → Approve
```

- **Selective context:** Choose files and directories through the TUI or VSCodium.
- **ZIP-in / ZIP-out:** Send actual archives and receive modified ones.
- **LLM integration:** OpenAI and configurable endpoints.
- **Streaming:** Follow progress and track token usage.
- **Git-friendly review:** See what changed and approve individual files.
- **Safe by default:** Nothing touches your working tree until you approve it.

## Getting started

Build and install:

```bash
go test ./...
go build -o zipdeck ./cmd/zipdeck
sudo install -m 755 zipdeck /usr/local/bin/zipdeck
```

Run the TUI:

```bash
OPENAI_API_KEY=sk-... zipdeck
```

Install the VSCodium extension:

```bash
codium --install-extension zipdeck-vscodium-0.2.0.vsix --force
```

Configure `~/.config/zipdeck/config.json`:

```json
{
  "provider": "openai-archive",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-5.2",
  "api_key_env": "OPENAI_API_KEY"
}
```

Set `OPENAI_API_KEY` in your environment before launching VSCodium. API usage is billed separately from ChatGPT.

## The idea

I like agents. I also like knowing exactly what's happening in my codebase.

Zipdeck is about **gated, semi-autonomous development**: let AI handle targeted changes while keeping context selection, review, and approval in your hands.

**Pick. ZIP. Prompt. Diff. Ship.**
## Where output goes

All generated archives and saved requests now default to:

```text
~/zipdeck/outputs/
├── my-project-selected-1234.zip
├── my-project.zip
├── my-project-llm.zip
└── jobs/
    └── my-project/
        └── job-XXXXXX/
            ├── input.zip
            ├── result.zip
            ├── prompt.txt
            ├── job.json
            └── review/
```

Override the directory for CLI, TUI and MCP with `ZIPDECK_OUTPUT_DIR=/some/path`.
The VSCodium extension has a `zipdeck.outputDirectory` setting; it passes that path
through to the Go subprocess. The project itself is not modified until approval.

## OpenCode / Hermes / MCP

Zipdeck includes a local stdio MCP server, **no extra Node/Python daemon required**:

```sh
zipdeck --mcp
```

### OpenCode

Add the following to your OpenCode `opencode.json` or `opencode.jsonc` configuration:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "zipdeck": {
        "type": "local",
        "command": ["/usr/local/bin/zipdeck", "--mcp"],
        "codemode": false
      }
    }
  }
}
```

### Hermes Agent

Install the same local MCP stdio server in Hermes:

```sh
hermes mcp add zipdeck --command /usr/local/bin/zipdeck --args --mcp
hermes mcp test zipdeck
```

If your Hermes version uses YAML configuration instead, register the same
executable and `--mcp` argument as a stdio MCP server.

### MCP tools

- `zipdeck_pack` — ZIP explicitly chosen project-relative paths.
- `zipdeck_submit` — ZIP chosen paths, send with a required prompt, save the returned ZIP and request history (requires a configured provider and API key).
- `zipdeck_jobs` — list project sessions.
- `zipdeck_review` — list changed files and safe local before/after snapshots.
- `zipdeck_apply` — apply **one** reviewed file; **disabled by default in MCP**.

Use absolute project roots. MCP clients supply selected file paths explicitly; Zipdeck
never grants an agent access to the entire project implicitly. All MCP results go through
the same archive validation and review logic as the standalone CLI.

To enable MCP application deliberately, start the server with
`ZIPDECK_MCP_ALLOW_APPLY=1`; otherwise use VSCodium or CLI approval:

```sh
zipdeck --review ~/zipdeck/outputs/jobs/my-project/job-XXXXXX
zipdeck --git-diff ~/zipdeck/outputs/jobs/my-project/job-XXXXXX
zipdeck --apply ~/zipdeck/outputs/jobs/my-project/job-XXXXXX --apply-file src/example.go
```

MCP integration provides **tools to an agent**; it does not itself run OpenCode or
Hermes as the LLM backend. OpenAI archive and custom multipart ZIP transports remain
the supported automated ZIP-in / ZIP-out providers. Other agents can invoke these
MCP tools to prepare context and review artifacts.

**Security:** MCP `zipdeck_submit` sends selected files to an external provider, which
may incur charges. Do not expose the local MCP server to untrusted clients. The
review and apply stages remain separate, with checksum conflict protection.
