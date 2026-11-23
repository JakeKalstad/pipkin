# 🦦 Pipkin

**Pick. Pack. Prompt. Inspect.**

Meet **Pip**, a little parcel-carrying otter who takes *just the code you select* to an LLM and brings back a ZIP you can inspect before applying.

No wandering agent. No mystery edits. Just deliberate, reviewable changes.

## Build and install

```sh
make test
make   # builds and installs pipkin, the VSCodium extension
```

## Configure

```sh
mkdir -p ~/.config/pipkin
cp .env.example ~/.config/pipkin/.env
chmod 600 ~/.config/pipkin/.env
# Edit ~/.config/pipkin/.env to add your actual keys.
```

Pipkin loads `~/.config/pipkin/.env` and the current project's `.env` **without overriding exported variables**. To load a specific file instead, set `PIPKIN_ENV_FILE=/path/to/.env`. The `.env` parser supports plain `KEY=value`, optional `export`, single/double quotes, and comments (not shell evaluation).

Provider settings live in `~/.config/pipkin/config.json`. `PIPKIN_CONFIG` can select another config file.

Example provider configuration (`~/.config/pipkin/config.json`):

```json
{
  "provider": "openai-archive",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-5.2",
  "api_key_env": "OPENAI_API_KEY",
  "langfuse_url": "http://127.0.0.1:3000",
  "langfuse_public_key_env": "LANGFUSE_PUBLIC_KEY",
  "langfuse_secret_key_env": "LANGFUSE_SECRET_KEY"
}
```

Never commit actual API keys. `.env` files are ignored; `.env.example` is safe to commit.

## Optional web review

Set in your `.env`:

```dotenv
LISTEN_URL=http://127.0.0.1:8787
```

Start the standalone web viewer:

```sh
pipkin --web
```

Visit <http://127.0.0.1:8787>. You can also run `pipkin` normally; it starts the web viewer alongside the interactive TUI when `LISTEN_URL` is set.

The Go `html/template` interface lists saved deliveries, displays submitted ZIP vs result and current workspace vs result side-by-side, and reports model usage. **It is read-only and intentionally bound to loopback** (no password or network exposure). To approve files, use the VSCodium review panel or `pipkin --apply`.

## Existing workflow

- Use the interactive TUI to check/uncheck files, then ZIP or send a prompt.
- In VSCodium, right-click files → **Pipkin: Send Selected Files to LLM**.
- Review a session in **Pipkin Deliveries**, inspect native diffs and approve individual files.
- OpenCode and Hermes can use `pipkin --mcp` with the `pipkin_pack`, `pipkin_submit`, `pipkin_jobs`, `pipkin_review`, and `pipkin_apply` tools.
- Archives and sessions default to `~/pipkin/outputs` (`/home/jake/pipkin/outputs` on your machine). Override using `PIPKIN_OUTPUT_DIR`.

```sh
pipkin --workflow --files-from /tmp/files.json --prompt-file /tmp/prompt.txt .
pipkin --review ~/pipkin/outputs/jobs/PROJECT/job-ID
pipkin --git-diff ~/pipkin/outputs/jobs/PROJECT/job-ID
```

**Pip's rule:** he delivers changes; *you* decide what gets committed.

## Terminal interface

On wide terminals, the file picker shares the screen with Pip, recent ZIPs, and saved requests. All LLM submissions—including the legacy text provider—are now recorded as **reviewable sessions**.

**File picker:** `space` select, `z` package, `p` prompt and send, `r` review completed deliveries, `i` import an existing `<project>.zip` + `<project>-llm.zip` pair from the output directory, `q` quit.

**Review screen:** press `r`, select a delivery with the arrow keys and Enter. The left pane lists changed files; the right pane shows the actual unified Git diff (green additions, red deletions). On smaller terminals, press Enter to open a full-screen diff.

| Key | Action |
|---|---|
| `↑` / `↓` | Select a changed file |
| `Tab` | Switch between file navigation and diff scrolling |
| `o` | **OK** the selected change |
| `x` | Reject the selected change |
| `u` | Clear that decision |
| `A` / `X` | Approve all / reject all |
| `v` | Toggle submitted ZIP vs result / current workspace vs result |
| `c` | **Copy** approved changes into your project (requires every file to be decided and a separate `Y` confirmation) |
| `b` / `q` | Back / exit the reviewer |

Decisions are stored under each delivery as `decisions.json` and survive closing Pipkin. Rejected files are never copied. Before copying, Pipkin checks **all** destination paths and original SHA-256 hashes; a conflicting local edit stops the entire copy. Previous file contents are backed up inside the session. Copying does not commit or stage changes—review with `git diff` and run your tests before committing.

The web viewer and VSCodium diff viewer continue to work as before.
