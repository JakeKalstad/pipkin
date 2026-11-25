# Pipkin

**Work on software, research, and documents with local or hosted models.**

Pipkin is an open-source workspace tool for focused AI tasks. Choose the files a model should see, ask a question or request a change, and review proposed edits before applying them. Start a research workspace, keep a conversation going, or turn selected files into a ready-to-serve MkDocs site.

<p align="center">
  <img src="images/logo.svg" alt="Parcel, the Pipkin otter" width="96">
</p>

- **Choose the context.** Send the files relevant to the task so you can work on a large codebase without sending the whole repository on every request. For a question about a few lines, send a snippet.
- **Review before applying.** Pipkin saves the original selection, prompt, and result. Compare the returned files with the submitted ZIP or your current workspace, approve changes file by file, and apply them when you’re ready.
- **Spend tokens deliberately.** Start with one pass. Opt into draft and review passes for harder changes. Text requests keep stable file context ahead of the task to help providers that support prefix caching, and Pipkin records reported token usage.

I built Pipkin after a few tools I’d written turned into a Rube Goldberg machine. I wanted the same workflow in a terminal, browser, and VSCodium. **Parcel** is the otter who carries the ZIP.

Yes, this does involve reading a diff. We appreciate your patience with this legacy workflow. In recognition of this inconvenience, we’ve provided a TUI and an animal mascot.


![Pipkin landing screen](images/landing.png)

## Choose a workflow

| I want to… | Start here |
| --- | --- |
| Edit code or text files and review the diff | [Day-to-day workflow](#day-to-day-workflow) |
| Start a research, software, pitch, or job-search workspace | [Project profiles](#project-profiles) |
| Ask a question with optional file context and resume it later | [Free prompt](#free-prompt) |
| Give the model project rules or reusable skills | [System instructions and Markdown sources](#system-instructions-and-markdown-sources) |
| Turn project files into a documentation or research site | [Export an MkDocs site](#export-an-mkdocs-site) |
| Preview a spreadsheet or edit cells in a copy | [CSV and XLSX spreadsheets](#csv-and-xlsx-spreadsheets) |
| Connect another tool | [HTTP API](#web-http-api) or [MCP](#mcp) |

From an installed binary, run `pipkin .` for the terminal picker. To open the browser interface, set `LISTEN_URL=http://127.0.0.1:8787` and run `pipkin --web .`. Choose a [provider profile](#switch-between-model-providers) before sending a model request. Workspace files and saved history live locally. The selected provider determines where inference runs.

## Day-to-day workflow

- **Web:** Filter file paths, select the files you want, and choose a provider profile for the delivery. Submitting opens the saved job and shows progress. In review, jump to a file, show pending files, or reuse a delivery’s prompt and selection in a new draft.
- **Terminal:** Press `s` to select only the highlighted file or folder. Press Space to toggle an item in the current selection. Press `m` to switch between configured provider profiles for the current session. Press `r` to open delivery history, including failed attempts and their error details.
- **VSCodium:** **Send Selected Files to LLM** lets you choose Standard or Agentic. Set `pipkin.providerProfile` to use a specific profile for editor requests. **Pipkin Deliveries** lists jobs for the active workspace in newest-first order. Open a job to revisit its prompt, diff, or error.

### Hidden files

The terminal and web pickers hide dotfiles and dot-directories by default. A parent-directory selection also skips hidden descendants. To edit CI files, press `H` in the terminal or click **Show hidden files** in the web picker, then select `.github` or the specific files you need. For CLI manifests, pass `--show-hidden` and list the desired hidden paths explicitly. Pipkin always excludes `.git` and private `.env` files from selections and archives. Environment templates such as `.env.example` require the same opt-in as other dotfiles.

### Git history

Press `g` on a file or folder in the terminal picker, or `G` anywhere in the picker for the entire repository. In the browser, click **Git** next to a path or **Repository Git history** above the picker.

- Select a commit to see its patch. In the terminal, press **Enter**.
- Choose **Local commits** (`l`) for commits the upstream branch does not contain.
- Choose **Upstream diff** (`u`) to compare the upstream tip with your local branch tip.
- Choose **Working diff** (`w`) for staged and unstaged changes to tracked files.

Pipkin reads the locally available upstream ref and falls back to `origin/<current-branch>` if present. These views are read-only and use the 100 most recent matching commits.

## Build and install

```sh

make test
make   # builds and installs Pipkin and the VSCodium extension
```

## Download or run in Docker

Download a Linux or macOS binary from [GitHub Releases](https://github.com/JakeKalstad/pipkin/releases), or install the latest release into `~/.local/bin`:

```sh

curl -fsSL https://github.com/JakeKalstad/pipkin/releases/latest/download/install.sh | sh
```

For a browser-based setup, use the [Docker Compose cheat sheet](#docker-compose-cheat-sheet) below.

## Docker Compose cheat sheet

Run these commands from the Pipkin checkout with Docker Compose 2.30 or later. The default web UI is <http://127.0.0.1:8787>. The host port is bound to loopback. The optional vLLM container requires an NVIDIA GPU and NVIDIA Container Toolkit.

### 1. Choose the workspace and provider

In the **Pipkin checkout’s** `.env`, set the workspace you want to edit. If you’re happy using the checkout itself, leave `PIPKIN_WORKSPACE` unset.

```dotenv
PIPKIN_WORKSPACE=/absolute/path/to/your/project
PIPKIN_PORT=8787
PIPKIN_UID=1000
PIPKIN_GID=1000
```

On Linux, use `id -u` and `id -g` to find the IDs for your user. Compose mounts the project at `/workspace` and saves deliveries in its `.pipkin/outputs` directory.

For named profiles, put `config.json` in the checkout’s `deploy/` directory or set `PIPKIN_CONFIG_DIR` to the host directory containing it. Set `PIPKIN_PROVIDER_PROFILE=claude` (or another profile name) in the checkout’s `.env` and add the provider key there. When no named profile is selected, use `PIPKIN_PROVIDER`, `PIPKIN_BASE_URL`, and `PIPKIN_MODEL` instead. See [Switch between model providers](#switch-between-model-providers) for complete profiles and key names. For a separate runtime environment file, set `PIPKIN_DOCKER_ENV` to its path.

### 2. Pick a stack

| Run | Command |
| --- | --- |
| Pipkin from source, with a hosted provider or existing reachable inference endpoint | `docker compose up -d --build pipkin` |
| Pipkin from the published GHCR image | `docker compose pull pipkin` then `docker compose up -d --no-build pipkin` |
| Pipkin without LangGraph dependencies | `PIPKIN_WITH_LANGGRAPH=false docker compose up -d --build pipkin` |
| Pipkin + bundled vLLM | `docker compose --profile vllm up -d --build pipkin vllm` |
| Pipkin + local Langfuse | `pipkin_lf up -d --build pipkin langfuse-web langfuse-worker` |
| Pipkin + local Langfuse + vLLM | `pipkin_lf --profile vllm up -d --build pipkin langfuse-web langfuse-worker vllm` |

The GHCR command requires a published image. Set `PIPKIN_IMAGE` to a specific GHCR tag if you want a pinned build. The final two rows use the shell function defined below. LangGraph can be enabled on any row except the explicitly slim build.

### vLLM in Compose or on another machine

For bundled vLLM, add the following to the checkout’s `.env`:

```dotenv
VLLM_MODEL=Qwen/Qwen3-0.6B
VLLM_MAX_MODEL_LEN=8192
PIPKIN_PROVIDER=vllm
PIPKIN_BASE_URL=http://vllm:8000/v1
PIPKIN_MODEL=Qwen/Qwen3-0.6B
```

If using a named profile, set its model and base URL to these values. Watch `docker compose logs -f vllm` until the model is loaded. The bundled vLLM port is internal to the Compose network. Its downloaded model weights persist in a volume.

For an existing server, use the **Pipkin-only** command and set the endpoint to an address the container can reach. In a container, `127.0.0.1` refers to that container. For an existing vLLM on the same host, Docker Desktop provides `host.docker.internal`. On Linux, you can create a small `compose.host.yaml` overlay to add the host-gateway name:

```yaml
services:
  pipkin:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

Start with `docker compose -f compose.yaml -f compose.host.yaml up -d --build pipkin` and set the profile URL to `http://host.docker.internal:9999/v1` if your server uses port 9999. The host model server must listen on an address reachable from Docker. Keep its port restricted to the networks that need it.

### Enable LangGraph

LangGraph runs inside the Pipkin container. For a source build with the default options or the published image, merge [the LangGraph profile example](deploy/config.langgraph.example.json) into your mounted `config.json`. Remove any host-specific `langgraph_python` path or set it to `/opt/langgraph/bin/python`. Select **Agentic mode** when submitting in the web UI.

```sh
docker compose exec pipkin python3 -c 'from langgraph.graph import StateGraph'
docker compose exec pipkin pipkin --agent-profiles
```

### Connect Langfuse or run it locally

For an existing HTTPS instance, set `LANGFUSE_BASE_URL`, `LANGFUSE_PUBLIC_KEY`, and `LANGFUSE_SECRET_KEY` in Pipkin’s runtime environment. Use the Pipkin-only or vLLM row above.

For a local stack, download the upstream Compose file and generate its infrastructure credentials once. The following uses `openssl` and keeps those credentials in a separate file:

```sh
mkdir -p deploy
touch .env
curl -fsSLo deploy/langfuse.upstream.yaml \
  https://raw.githubusercontent.com/langfuse/langfuse/v4.85.0/docker-compose.yml

if [ ! -e .env.langfuse ]; then
  (
    set -eu
    umask 077
    postgres=$(openssl rand -hex 24)
    minio=$(openssl rand -hex 24)
    cat > .env.langfuse <<EOF_INNER
LANGFUSE_IMAGE_TAG=4.85.0
NEXTAUTH_URL=http://localhost:3000
NEXTAUTH_SECRET=$(openssl rand -hex 32)
SALT=$(openssl rand -hex 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$postgres
DATABASE_URL=postgresql://postgres:$postgres@postgres:5432/postgres
CLICKHOUSE_PASSWORD=$(openssl rand -hex 24)
REDIS_AUTH=$(openssl rand -hex 24)
MINIO_ROOT_PASSWORD=$minio
LANGFUSE_S3_EVENT_UPLOAD_SECRET_ACCESS_KEY=$minio
LANGFUSE_S3_MEDIA_UPLOAD_SECRET_ACCESS_KEY=$minio
LANGFUSE_S3_BATCH_EXPORT_SECRET_ACCESS_KEY=$minio
EOF_INNER
  )
fi

pipkin_lf() {
  docker compose --env-file .env --env-file .env.langfuse \
    -f compose.yaml -f deploy/langfuse.upstream.yaml -f compose.langfuse.yaml "$@"
}

pipkin_lf up -d --build pipkin langfuse-web langfuse-worker
pipkin_lf ps
```

Open <http://localhost:3000>, create a project and its API keys, then add `LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY` to the Pipkin runtime environment file. Omit `langfuse_url` from the mounted JSON when using the overlay. The overlay supplies `http://langfuse-web:3000` inside Docker and shows a loopback link in the UI. Recreate Pipkin to pick up the keys:

```sh
pipkin_lf up -d --no-deps --force-recreate pipkin
```

For the full stack with vLLM, also add the vLLM settings above, then run:

```sh
pipkin_lf --profile vllm up -d --build pipkin vllm langfuse-web langfuse-worker
pipkin_lf --profile vllm logs -f pipkin vllm langfuse-web
```

### Check, restart, or stop

```sh
docker compose ps
docker compose logs --tail=100 pipkin
curl -fsS -o /dev/null http://127.0.0.1:8787/
docker compose exec pipkin pipkin --provider-profiles
docker compose up -d --force-recreate pipkin
docker compose --profile vllm down
```

For the Langfuse stack, use `pipkin_lf` in place of `docker compose` with the same profile you started. After a delivery, look for **Langfuse accepted the trace export** in the Pipkin review. If the project picker shows the wrong files, check `PIPKIN_WORKSPACE`. If copying reports permission errors, check the configured UID and GID. Plain `down` keeps model and Langfuse data volumes. Adding `-v` deletes those volumes.

## Quick start with a local model

With Pipkin installed and your vLLM server running, create `~/.config/pipkin/config.json` or merge these fields into your existing config:

```json

{
  "provider": "vllm",
  "base_url": "http://127.0.0.1:8000/v1",
  "model": "your-served-model"
}
```

Use your server’s port and exact served model ID. If it requires authentication, set `"api_key_env": "VLLM_API_KEY"` and add the key to your Pipkin `.env`. A loopback server without authentication needs no API key.

Run `pipkin` from your project directory. Select the files you want the model to see, press `p` to send a prompt, and press `r` to review the delivery. In VSCodium, highlight code and run **Pipkin: Ask LLM About Selection** to ask a question without applying any changes.

## End-to-end tests

Run `make test-e2e` on a Unix host with Python 3 and `stty`. The suite exercises the terminal, web, snippet, and MCP entry points with temporary workspaces and local mock providers. No API keys or hosted services are required.

## Configure

Provider settings live in `~/.config/pipkin/config.json`. Set `PIPKIN_CONFIG` to use a different file. See [Switch between model providers](#switch-between-model-providers) for OpenAI, Grok, Claude, DigitalOcean, and vLLM profiles.

For API keys and optional settings such as the web UI and Langfuse, use a Pipkin environment file.

```sh

mkdir -p ~/.config/pipkin
cp -n .env.example ~/.config/pipkin/.env
chmod 600 ~/.config/pipkin/.env
```

Pipkin loads the home `.env` and then the current directory’s `.env`. Exported variables take precedence, followed by the home file. To load a specific file, export `PIPKIN_ENV_FILE=/path/to/.env` before launching Pipkin. The parser accepts `KEY=value`, optional `export`, quotes, and comments. Keep keys outside version control.

## Switch between model providers

Configure the providers you use once, then switch by profile name. The examples below cover OpenAI text and hosted archives, Anthropic Claude, xAI Grok, DigitalOcean Serverless Inference, and local vLLM.

### 1. Create or update your provider profiles

Open **Profiles** in the web header, or press `m` then `e` in the terminal. The editor saves named provider and agent profiles, their configured defaults, and the default provider's system prompt. Named providers can set `system_prompt`, `max_context_bytes`, and `max_output_tokens` alongside the model and endpoint. Agent profiles can set `system_prompt` for both draft and review in addition to their existing focus and review settings. Workspace guidance remains editable under **Instructions**. Use `api_key_env` to name a credential environment variable. Environment and CLI profile overrides still apply.

Create the directory if needed:

```sh
mkdir -p "$HOME/.config/pipkin"
```

If you already have a `config.json`, merge these two fields into it and keep your existing LangGraph and Langfuse settings. Otherwise, use this complete JSON as `~/.config/pipkin/config.json`. The default profile below is local. Replace any example model ID with the exact ID available to your account or local server.

```json
{
  "provider_profile": "local",
  "provider_profiles": {
    "local": {
      "provider": "vllm",
      "base_url": "http://127.0.0.1:8000/v1",
      "model": "Qwen/Qwen3-0.6B"
    },
    "openai": {
      "provider": "openai",
      "base_url": "https://api.openai.com/v1",
      "model": "gpt-5.2",
      "api_key_env": "OPENAI_API_KEY"
    },
    "openai-archive": {
      "provider": "openai-archive",
      "base_url": "https://api.openai.com/v1",
      "model": "gpt-5.2",
      "api_key_env": "OPENAI_API_KEY"
    },
    "claude": {
      "provider": "anthropic",
      "base_url": "https://api.anthropic.com/v1",
      "model": "claude-sonnet-4-6",
      "api_key_env": "ANTHROPIC_API_KEY",
      "max_output_tokens": 8192
    },
    "grok": {
      "provider": "xai",
      "base_url": "https://api.x.ai/v1",
      "model": "grok-4.3",
      "api_key_env": "XAI_API_KEY"
    },
    "do": {
      "provider": "digitalocean",
      "base_url": "https://inference.do-ai.run/v1",
      "model": "openai-gpt-oss-120b",
      "api_key_env": "DIGITALOCEAN_INFERENCE_KEY"
    }
  }
}
```

If you set `PIPKIN_CONFIG`, edit the file it points to. Use a base URL ending in `/v1`. Pipkin appends the API route.

### 2. Configure credentials and the local server

Add only the keys for providers you plan to use to `~/.config/pipkin/.env`. Replace the placeholder values. In JSON, `api_key_env` is the **name** of the variable containing the key.

```dotenv
# OPENAI_API_KEY=replace-with-your-openai-api-key
# ANTHROPIC_API_KEY=replace-with-your-anthropic-api-key
# XAI_API_KEY=replace-with-your-xai-api-key
# DIGITALOCEAN_INFERENCE_KEY=replace-with-your-digitalocean-inference-key
# VLLM_API_KEY=replace-with-your-local-server-key
```

Uncomment the applicable lines and run `chmod 600 "$HOME/.config/pipkin/.env"`.

| Profile | Where to get the key and what to set |
| --- | --- |
| `openai` | Create a project API key on the OpenAI API platform and set `OPENAI_API_KEY`. Standard edits use text requests. Agentic edits at the official OpenAI endpoint use the hosted archive workflow. |
| `openai-archive` | Uses the same project key. Choose a model with Code Interpreter support. Standard and agentic file-edit deliveries upload the selected ZIP to OpenAI. Snippets use the text API. |
| `claude` | Create a Claude API key and set `ANTHROPIC_API_KEY`. Pipkin uses the native Messages API. Increase `max_output_tokens` within your model’s limits if a large edit stops at the output limit. |
| `grok` | Create an xAI inference API key and set `XAI_API_KEY`. Use a Grok model that supports Chat Completions for your account. |
| `do` | Create a Serverless Inference model access key and set `DIGITALOCEAN_INFERENCE_KEY`. Use the exact model ID from DigitalOcean’s Model Catalog. A third-party model served by DigitalOcean still uses the `digitalocean` provider and its key. |
| `local` | Start vLLM and match the port and served model ID. A local server without API-key authentication needs no key. If your server requires one, add `"api_key_env": "VLLM_API_KEY"` to the local profile and set the matching key in `.env`. |

**Provider references** · [OpenAI GPT-5.2](https://developers.openai.com/api/docs/models/gpt-5.2) · [Claude model IDs](https://platform.claude.com/docs/en/about-claude/models/model-ids-and-versions) · [xAI Chat Completions](https://docs.x.ai/developers/rest-api-reference/inference/chat-completions) · [DigitalOcean model catalog](https://docs.digitalocean.com/products/inference/details/models/) · [vLLM serving](https://docs.vllm.ai/en/stable/serving/openai_compatible_server/)

If vLLM is installed and you need to start a test server, run this in a separate terminal:

```sh
vllm serve Qwen/Qwen3-0.6B --host 127.0.0.1 --port 8000 --max-model-len 8192
```

Once the model is loaded, check the served ID:

```sh
curl -fsS http://127.0.0.1:8000/v1/models
```

If your existing vLLM server uses port 9999, change the profile and check that port instead. Inside the Pipkin Compose stack, use `http://vllm:8000/v1` and match the Compose `VLLM_MODEL`. See [Deploy Pipkin](deploy/README.md).

### 3. Verify and switch

```sh
pipkin --provider-profiles
pipkin --provider-profile claude --provider-profiles
pipkin --provider-profile grok
```

The list command confirms profile selection without making a provider request. For a small real request that writes no project files, run:

```sh
printf '%s\n' '{"system":"Keep the reply short.","user":"Reply with OK."}' |
  pipkin --provider-profile claude --snippet
```

Replace `claude` with `grok`, `do`, `openai`, or `local` to check the other connections. The snippet test checks the text API. To verify hosted archive support, select a small disposable text file and submit an edit using the `openai-archive` profile, then open the review and inspect the returned diff.

Set `provider_profile` in JSON for a default, or set `PIPKIN_PROVIDER_PROFILE=do` in the home `.env` for an environment override shared by the CLI, TUI, web UI, editor, and MCP. The command-line flag wins for that process. Restart running Pipkin processes after changing `.env`. Named profiles take precedence over legacy provider, endpoint, model and key overrides. Agent profiles separately control the review runner and depth.

For authentication errors, check the selected profile and its `api_key_env`. For a model error, check the ID and account access. For a local connection error, verify that the server is still running at the profile’s host and port.

## Optional web interface

Set in your `.env`:

```dotenv

LISTEN_URL=http://127.0.0.1:8787
```

Start the standalone web interface:

```sh

pipkin --web
```

Visit <http://127.0.0.1:8787>. You can also run `pipkin` normally; it starts the web viewer alongside the interactive TUI when `LISTEN_URL` is set.

The Go `html/template` interface offers a recursive file/directory picker, prompt submission, saved deliveries, and highlighted unified diffs comparing the result with the submitted ZIP or the current workspace. Approve or reject each proposed file, then explicitly **Copy approved changes** to your working tree. Copying uses the same hash checks, conflict protection, and backups as the TUI. The browser runs only on loopback, uses per-process form tokens, and must not be exposed publicly without authentication. LLM submissions run in the background while the browser stays responsive.

![Pipkin web interface with the file picker and prompt form](images/webui_landing.png)

![Reviewing a diff and approving changes in the Pipkin web interface](images/webui_approve_diff.png)

With `LISTEN_URL`, `langfuse_url`, or `LANGFUSE_BASE_URL` configured, Pipkin displays the corresponding service links in the web header and the terminal picker. Terminals that support OSC 8 make the displayed URLs clickable.

### System instructions and Markdown sources

Pipkin loads project guidance by default for text and hosted archive deliveries and Free prompt. It uses root `AGENTS.md` when customised and the built-in default when the file is missing, blank, or contains the original starter template. The web Instructions page shows which guidance will be used. Use it for build commands, project conventions, and review rules. To opt out, select **Skip automatic project guidance** under **Instructions**. Explicit Markdown paths remain available for project-specific layouts.

Store reusable Agent Skills as `.agents/skills/<skill-name>/SKILL.md`. Pipkin lists these skills and sends their Markdown when you enable them. Use **Instructions** in the web UI to create or edit either format and to enable or disable skills. The browser editor supplies starter text, including `name` and `description` front matter for a new skill. The text is saved directly in the workspace and can be checked into Git.

In the terminal, press `I` to open the instruction picker. Press Enter to edit, `n` to start a new skill, or Space to toggle a skill. Editing uses the [configured editor](#default-editor). Save and exit the editor to return to the instruction list. A new skill starts disabled and is highlighted immediately, even when you keep the starter template. Press Space to enable it.

You can still add a workspace system prompt, extra Markdown paths, or per-delivery guidance. Instructions are loaded independently of ZIP selection. Pipkin reads skill Markdown as guidance. Skill scripts, references, and assets are not discovered or executed automatically. **Help** documents the interfaces, and **About** displays build and provider details.

```sh
pipkin --workflow --agentic --files-from files.json --prompt-file task.txt \
  --instructions AGENTS.md --instructions .claude/skills/review/SKILL.md .
```

The workspace prompt comes first, followed by root `AGENTS.md`, saved Markdown paths, per-delivery Markdown paths, and per-delivery inline text. Pipkin keeps the first occurrence of each path. The combined guidance is limited to 32 KiB and up to 16 listed files. Pipkin refuses paths outside the workspace, symlinks, and protected paths. A missing or invalid instruction file stops the delivery before a model call.

Pipkin sends the same instructions for the initial draft, subsequent reviews, and hosted-to-text fallback. The provider receives the guidance in the system instructions alongside Pipkin's format and tool constraints. Text providers deduct custom guidance from the available file-context budget. Custom system guidance is supported by text and hosted archive providers. The legacy multipart transport and editor snippets use their existing interfaces.

Saved guidance is scoped to the workspace's resolved path and shared by Pipkin clients using the same `PIPKIN_OUTPUT_DIR`. Review records include source names and SHA-256 fingerprints. Reusing a delivery loads the current workspace guidance. Re-enter any one-off guidance you still need. If Langfuse request-content capture is enabled, captured provider inputs can include the guidance.

### Default editor

Set `"editor": "vscodium"` in `~/.config/pipkin/config.json` (or the file named by `PIPKIN_CONFIG`). The same setting is available in **Profiles**, or by pressing `m` then `e` in the terminal. It applies across workspaces to terminal edits of text files, instructions, and profile settings.

Pipkin runs `codium --wait`. With `codium` on your PATH, save and close the edited tab to return to Pipkin and apply the edit. Set `"editor": "auto"` or leave the setting blank to use `VISUAL`, then `EDITOR`, then `vi`.

### Free prompt

Press `f` in the terminal or choose **Free prompt** in the web header. In the TUI history, Enter opens a conversation, `n` starts a new one, and `d` followed by uppercase `Y` deletes it. Inside the composer, Enter adds a line, Ctrl+S sends, Ctrl+T opens the scrollable transcript, and Esc returns to history. In the browser, use the **Conversations** sidebar to open, start, or delete a conversation.

Use **Choose context files** in the web composer or Ctrl+F in the terminal composer to attach workspace text files. In the terminal file list, Space toggles each file and `p` copies the main picker's selection. Ctrl+P opens the model and profile menu. Context is opt-in for each message, limited by the selected provider's context budget and 256 KiB per file. Pipkin reads the files at send time and leaves their contents out of the local conversation transcript.

Successful exchanges are saved locally per workspace and sorted by recent activity. The next model call includes the latest complete turns that fit the context budget. The saved transcript keeps earlier turns. Free prompts use workspace guidance and create no ZIP delivery.

For HTTP, use `GET /prompt` for history or `GET /prompt?conversation=ID` for a transcript. POST an optional `conversation` field to continue a thread or omit it to start one. Use `action=delete` and the conversation ID to delete. POST uses URL-encoded fields and the form's CSRF token. HTML submissions redirect to the conversation, while `Accept: application/json` returns `{"reply":"...","conversation":"ID"}`. JSON GET requests return the history or transcript.

For MCP, call `pipkin_prompt` with an absolute `root`, a `prompt`, and the optional `conversation` ID. Use `pipkin_conversations` with `root` and `action` (`list`, `read`, or `delete`). Read and delete also require `conversation`. HTTP and MCP prompt submissions accept optional `provider_profile`, `system_prompt`, and `instruction_files` (repeated form fields over HTTP, an array over MCP). Send optional context paths as repeated `files` form fields or as `paths` to the MCP `pipkin_prompt` tool.

### Project profiles

Open **Project** in the web UI or press `J` in the TUI. Pipkin checks the workspace root for software manifests and source files. Other project guesses can use recognizable directory names. Ambiguous workspaces use General. The initial profile is recorded in `.pipkin-project.json` on interactive startup, and an explicit selection replaces the guess.

| Profile ID | Project type |
| --- | --- |
| `general` | General |
| `software` | Existing software |
| `software-new` | New software |
| `research` | Research |
| `pitch` | Grant, marketing, seed, or sales package |
| `job-search` | Job search |

The selected profile contributes guidance alongside `AGENTS.md` and workspace instructions. Initialisation creates a local Git repository when needed and writes a profile-specific starter README if one is absent. It leaves existing README files intact.

```sh
pipkin --project-profiles
pipkin --project .
pipkin --project-profile research .
pipkin --init-project pitch ./seed-round
```

The HTTP interface uses `GET /project` and URL-encoded `POST /project` with `profile` and optional `action=init`. MCP exposes `pipkin_project(root, action, profile?)` with actions `show`, `set`, and `init`.

### CSV and XLSX spreadsheets

Highlight a CSV and press `W` to open the grid. Use the arrow keys to move, Enter to edit a cell, and `s` to name and save a new CSV. `v` opens the raw text preview. CSV support uses the Go standard library. Web, CLI, and MCP use the same cell-edit format as XLSX, except `sheet` is optional (use `CSV` if supplied). CSV edits work on existing cells and use scalar values only.

For XLSX, run `make install-xlsx` from the Pipkin source directory. This installs openpyxl in the `xlsx-venv` folder beside Pipkin's config. You can also set `PIPKIN_XLSX_PYTHON` to your own interpreter with openpyxl 3.1 or later. **Workbook** in the web UI previews sheet names, dimensions, and the first 20 rows and 12 columns of up to 20 sheets. To edit, supply a JSON array of cells and a new output path. Pipkin returns the before-and-after values and writes a separate copy. The source stays intact.

```json
[
  {"sheet":"Budget","cell":"B2","value":4500},
  {"sheet":"Budget","cell":"B5","kind":"formula","value":"=SUM(B2:B4)"}
]
```

```sh
pipkin --workbook budget.xlsx .
pipkin --workbook budget.xlsx --workbook-edits edits.json --workbook-output budget-reviewed.xlsx .
```

Use `null` to clear a cell. Strings are literal unless you specify `kind: "formula"`. Formula expressions are saved for the spreadsheet application to calculate. The editor is for cell values and formulas in existing sheets. Advanced workbook features may be rejected if the library cannot preserve them.

Use `GET /workbook` for the form and `POST /workbook` with `file` and optional `output` and `edits` (JSON) plus the form's CSRF token. MCP exposes the same operation as `pipkin_workbook(root, file, output?, edits?)`, with edits given as an array. In the TUI, highlight an XLSX workbook and press `W` or `e`. Choose a worksheet if prompted. Use the arrows to move, Enter to edit, and `s` to save a separate XLSX copy. The grid supports up to 200,000 cells per worksheet. Entries such as `12.5` become numbers, entries beginning with `=` become formulas, and an empty entry clears the cell.

### Export an MkDocs site

Select the source files for the site and describe what readers should learn. For example, ask for an onboarding guide with setup and usage pages, or a research summary with findings, evidence, and limitations. Press `D` in the TUI or choose **Export MkDocs ZIP** in the web picker. The text provider receives the selected text plus the workspace's profile and instructions. The archive contains the generated Markdown, navigation, configuration, dependencies, and serving instructions. The selected files remain in the workspace, and any context omissions are listed in the exported README.

```sh
pipkin --mkdocs --files-from files.json --prompt-file docs-brief.md .
```

CLI and MCP return the saved ZIP path under the Pipkin output directory's `mkdocs` folder. Unzip the archive, then run these commands in the extracted directory:

```sh
python3 -m pip install -r requirements.txt
mkdocs serve
```

Run `mkdocs build` in the same directory if you want static HTML in `site/`. The export is a source site using the built-in MkDocs theme and generated Markdown pages. It uses a single documentation-generation pass.

**HTTP:** Send a URL-encoded `POST /mkdocs` with the file picker's `csrf`, repeated `files`, and `prompt`. Optional fields include `provider_profile`, `system_prompt`, repeated `instruction_files`, and `show_hidden=1`. Success returns a ZIP attachment.

**MCP:** Call `pipkin_mkdocs(root, paths, prompt)` with optional `provider_profile`, `system_prompt`, and `instruction_files`. It returns `zip` and any `omitted` paths.

## Web HTTP API

Start with `pipkin --web` and the loopback `LISTEN_URL` shown above. These endpoints serve HTML and accept URL-encoded form data. For every POST, include the `csrf` token from the hidden field on the page for that action. Delivery and instruction mutations redirect on success. Free prompts return replies directly when the client requests JSON.

| Method and path | Parameters | Behaviour |
| --- | --- | --- |
| `GET /` | Optional `hidden=1` | Show the workspace file picker and saved deliveries. |
| `GET /git` | Optional `path`, `local=1`, `diff=upstream\|working\|commit`, `commit` (full object ID) | Show scoped Git history or a read-only patch. |
| `GET /help` and `GET /about` | — | Show setup, usage, API and MCP references or application metadata. |
| `GET /prompt` | Optional `conversation` | Open conversation history or a saved transcript. |
| `POST /prompt` | `prompt`, optional `conversation`, `provider_profile`, `system_prompt`, repeated `instruction_files` and `files`. To delete, send `action=delete` and `conversation`. | Continue or create a conversation, or delete it. HTML submissions redirect. JSON submissions return the result. |
| `GET /project`, `POST /project` | POST accepts `profile` and optional `action=init`. | Show or set the workspace profile and optionally initialise Git. |
| `GET /workbook`, `POST /workbook` | POST accepts `file` and optional `output` and `edits` (JSON). | Preview a CSV or XLSX spreadsheet or edit cells in a separate copy. |
| `POST /mkdocs` | `prompt`, repeated `files`, optional `provider_profile`, `system_prompt`, repeated `instruction_files`, `show_hidden=1` | Download a ready-to-serve MkDocs ZIP. |
| `GET /instructions` | — | Open the workspace instruction editor. |
| `POST /instructions` | `system_prompt`, newline-separated `instruction_files`, optional `skip_project_instructions=1` | Save workspace guidance. Invalid guidance returns 422. |
| `GET /instructions/edit` | `path` or new `skill` name | Open or scaffold a standard Markdown file. |
| `POST /instructions/edit` | `path` and `markdown`, or `path` and `action=toggle` | Save Markdown or toggle an existing skill. |
| `POST /submit` | `prompt`, repeated `files`, optional `provider_profile`, `agentic=1`, `show_hidden=1`, `system_prompt`, repeated `instruction_files` | Start a delivery in the background. Refresh the landing page for its status. |
| `GET /review` | `job`, optional `mode=submitted\|current` | Show delivery status or compare the result with the submitted ZIP or current workspace. The default is `submitted`. |
| `POST /decision` | `job`, `action`, `path` for a single file, optional `mode` | Save a decision. Actions are `approve`, `reject`, `reset`, `approve-all`, and `reject-all`. |
| `POST /copy` | `job`, `confirm=COPY` | Copy approved files into the working tree. Returns `409` if a decision is pending or a workspace conflict blocks copying. |

The web endpoints use a `job` value of the form `PROJECT/JOB-ID`. Use the value from the delivery’s review link.

## MCP

See? We don’t hate agentic development. We just like reading the code sometimes.

Configure your MCP client to launch the local stdio server. Use the full path to the Pipkin executable if your client has a different `PATH`.

- **Command** `pipkin`
- **Arguments** `["--mcp"]`
- **Transport** stdio

| Tool | Required arguments | Result |
| --- | --- | --- |
| `pipkin_prompt` | `root`, `prompt` | Returns `reply` and `conversation`. Pass an existing conversation ID to continue it. |
| `pipkin_conversations` | `root`, `action` | List, read, or delete saved conversations. |
| `pipkin_instructions` | `root`, `action` | Lists or manages `AGENTS.md` and discovered skills. |
| `pipkin_project` | `root`, `action` | Show, set, or initialise the project profile. Set and init require `profile`. |
| `pipkin_workbook` | `root`, `file` | Preview a CSV or XLSX spreadsheet or edit it when `output` and `edits` are supplied. |
| `pipkin_mkdocs` | `root`, `paths`, `prompt` | Generate an MkDocs site and return its ZIP path and any omitted context files. |
| `pipkin_pack` | `root`, `paths` | Creates a ZIP and returns its path and file count. |
| `pipkin_submit` | `root`, `paths`, `prompt` | Sends the selection and returns `job` and `result`. Accepts optional `agentic` (boolean), `system_prompt` (string), and `instruction_files` (array of strings). |
| `pipkin_jobs` | `root` | Lists saved job directories for the project. |
| `pipkin_review` | `job` | Returns the saved job’s file changes for inspection. |
| `pipkin_apply` | `job`, `file` | Applies one reviewed file. Requires `PIPKIN_MCP_ALLOW_APPLY=1` in the server’s environment. |

Use absolute paths for `root` and `job`. Use project-relative paths for the `paths` array and the `file` argument. A typical session runs `pipkin_submit` → `pipkin_review`, then applies selected changes through a review interface or the opt-in MCP apply tool.

For `pipkin_instructions`, use `list`, `read`, `save`, or `toggle` as the action. The last three require `file`, a standard workspace-relative Markdown path. `save` also requires `markdown`. Confirm writes with the user before saving or toggling through an MCP client.

Example arguments to `pipkin_submit` with custom guidance:

```json
{
  "root": "/absolute/path/to/project",
  "paths": ["cmd/pipkin"],
  "prompt": "Implement the requested behaviour and check callers.",
  "agentic": true,
  "system_prompt": "Preserve compatibility with saved deliveries.",
  "instruction_files": ["AGENTS.md", ".claude/skills/review/SKILL.md"]
}
```

## Troubleshooting deliveries

- **Wrong model or provider in the TUI** means Pipkin loaded different settings than you expected. `make` installs the binary and extension. Provider settings are read from `PIPKIN_CONFIG` or `~/.config/pipkin/config.json` at startup. For named profiles, check `pipkin --provider-profiles`. Without a named profile, `PIPKIN_MODEL` and then `OPENAI_MODEL` take precedence over the configured model. Use an API model ID supported by the selected provider and restart Pipkin after editing the settings.
- **A selected file is missing from the model context** can occur with any text provider. Pipkin sends a text subset of the selected ZIP, limited to 50,000 bytes per file and `max_context_bytes` total (160,000 by default). Omitted paths are listed in the web review and in the delivery's `job.json` under `context_omitted`. Select only the files relevant to the task, or use `openai-archive` to send the selected ZIP as an attachment.
- **Provider returned identical files** means the result ZIP has the same paths and file contents as the selection you sent. Check that the selection includes the files your prompt asks to change and state the expected edit explicitly. For agentic jobs, compare the saved `input.zip`, `draft.zip`, and `result.zip` to see which pass returned the unchanged files.
- **Langfuse** requires a base URL and the project’s public and secret keys. Set `LANGFUSE_BASE_URL` in your Pipkin `.env`, or set `langfuse_url` in the provider config. Pipkin reports export status after the delivery in the TUI, the web review page, and `job.json`. Traces contain workflow metadata and usage by default. See **Model context and usage** for the text capture option.

## Agentic mode

Agentic mode is opt-in. With the `openai` provider pointed at OpenAI’s API, Pipkin uses the hosted code interpreter for two passes. The first produces a draft and the second compares it against the original ZIP, checks the requirements, and refines it. Both passes can run focused checks in the hosted interpreter. Existing `openai-archive` configs still use the hosted archive workflow for both standard and agentic requests.

vLLM, Grok, Claude, DigitalOcean, and custom OpenAI-compatible endpoints use text requests for agentic mode. The built-in runner uses two passes. The first implements a draft and the second reads the draft and original task to propose corrections. The text context limits apply to both passes. Neither text pass executes project code. In either mode, you approve the final diff before Pipkin copies files into your project.

| Interface | Enable agentic mode |
| --- | --- |
| Terminal | Press uppercase `P` to start an agentic prompt. |
| Web | Check **Agentic mode** before submitting. |
| CLI | Add `--agentic` to a `--workflow` command. |
| MCP | Pass `"agentic": true` to `pipkin_submit`. |

Agentic jobs have a 15-minute workflow deadline. If a hosted pass finishes without a generated ZIP, Pipkin switches to text edits for the rest of that delivery. The text context limits apply and the extra request can incur additional API usage. Hosted jobs accept ZIP, TAR, or gzip-compressed TAR results. Pipkin converts TAR results to ZIP before review. Pipkin saves the intermediate archive as `draft.zip` and totals reported token usage across all passes. A failed review leaves the job failed. The final ZIP uses the same approval workflow as other submissions.

### LangGraph profiles

LangGraph is an optional local runner for agentic jobs. Pipkin starts it for the delivery and retains responsibility for provider calls, packaging, and review. The built-in runner remains the default.

```sh

python3 -m venv ~/.config/pipkin/langgraph-venv
~/.config/pipkin/langgraph-venv/bin/python -m pip install -r requirements-langgraph.txt
```

Add these fields to your existing provider configuration. Use the absolute path to your virtual environment’s Python executable.

```json

{
  "langgraph_python": "/home/you/.config/pipkin/langgraph-venv/bin/python",
  "agent_profile": "quick",
  "agent_profiles": {
    "quick": {
      "runner": "langgraph",
      "review_passes": 1
    },
    "thorough": {
      "runner": "langgraph",
      "review_passes": 2,
      "review_focus": "Check regressions, edge cases, and compatibility."
    }
  }
}
```

Pipkin ships two LangGraph presets you can use without writing a profile. Install LangGraph first, then select one:

```sh
pipkin --agent-profile focused
pipkin --agent-profile thorough
```

**Focused** runs a draft and one review with up to 4 KiB of original-file references for text models. **Thorough** allows two reviews and up to 8 KiB of references. A no-change review ends remaining model calls. The actual reference allowance is also capped at one eighth of the text context budget. Pipkin sends original versions of changed text files that fit within that allowance. Hosted review can compare the full original and candidate archives.

Both profiles use Pipkin’s `engineering-v1` prompts. The draft instructions prioritize focused edits, existing conventions, and compatibility. The reviewer checks the candidate against the task and the evidence available. Hosted archive passes may run focused checks in the interpreter. Local text passes inspect the supplied code and cannot run project tests. The saved job records the selected profile and prompt preset.

For custom profiles, `draft_focus` and `review_focus` add project-specific instructions. Set `review_context_bytes` to 0–16384 for optional original-file references in text review. For example, use `"draft_focus": "Preserve public API signatures."` and `"review_focus": "Check cancellation and error paths."`.

Edit `agent_profiles` to add or update named profiles. Each LangGraph profile runs one draft followed by up to three review passes. Additional passes use the preceding revision and can incur additional model usage. Use `pipkin --agent-profiles` to list profiles and see which is active. Set `PIPKIN_AGENT_PROFILE=thorough` in your environment or pass `--agent-profile thorough` for one process. Select `builtin` to return to the built-in runner. The chosen profile applies to agentic submissions in the TUI, web UI, CLI, and MCP.

The terminal reviewer shows a single-line preview of the original prompt. In the web reviewer, expand **Original prompt** to read up to 4 KiB. The saved delivery retains the complete `prompt.txt`.

## Model context and usage

Text submissions sort the selected paths and place file contents before the task. Repeating a request against unchanged files gives prefix-caching providers the same starting context. Actual cache hits depend on the provider, model, request size, and cache availability. Pipkin records the provider’s input, output, and cached token counts in `job.json` and reports them to Langfuse when configured. Every model request gets its own generation span when Langfuse is configured. Saved deliveries group draft, review, fallback, and standard calls under one workflow trace. VSCodium selection requests use `pipkin-snippet`. Direct CLI calls also export a generation span. Each span records provider, model, timing, outcome, and token counts when the provider supplies them. Content capture is off by default. Set `PIPKIN_LANGFUSE_CAPTURE_IO=1` in your Pipkin `.env` and restart to populate generation **Input** and **Output** in Langfuse. Text requests can include selected source code and file replacements. Each field is limited to roughly 64 KiB. Hosted archive requests capture instructions and assistant text (or an artifact summary); archive bytes and authentication headers are excluded.

Hosted agentic runs reuse the original upload within a delivery and upload only the current draft for each subsequent review. A review that returns identical file contents and paths ends further model calls, even if the selected profile allows more passes. Review the resulting diff before applying it.

## Provider settings in .env

Uncomment one provider block in `.env.example`. Set `PIPKIN_PROVIDER`, `PIPKIN_BASE_URL`, and `PIPKIN_MODEL` together when switching endpoints. These settings override the corresponding values in `config.json`. `PIPKIN_API_KEY_ENV` names the environment variable that contains your key. Restart Pipkin after changing the environment.

## CLI workflow

- Use the interactive TUI to check/uncheck files, then ZIP or send a prompt.
- In VSCodium, right-click files → **Pipkin: Send Selected Files to LLM**.
- Review a session in **Pipkin Deliveries**, inspect native diffs and approve individual files.
- OpenCode and Hermes can use `pipkin --mcp` with the tools in the [MCP reference](#mcp).
- Archives and sessions default to `~/pipkin/outputs`. Override using `PIPKIN_OUTPUT_DIR`.

```sh

pipkin --workflow --files-from /tmp/files.json --prompt-file /tmp/prompt.txt .
pipkin --review ~/pipkin/outputs/jobs/PROJECT/job-ID
pipkin --git-diff ~/pipkin/outputs/jobs/PROJECT/job-ID
```

**Parcel delivers the ZIP. You decide what makes it into the commit.**

## Terminal interface

On wide terminals, the file picker shares the screen with Parcel, recent ZIPs, and saved requests. File-edit requests are saved as reviewable sessions.

| Key | Action |
| --- | --- |
| Space / `s` | Toggle selection / select only the highlighted item |
| `z` / `p` / `P` | Package a ZIP / send a file-edit request / draft and review in Agentic mode |
| `r` / `i` | Review deliveries / import an existing input and result ZIP pair |
| `g` / `G` / `H` | Git history for a path / repository history / toggle hidden files |
| `f` / `J` / `I` / `m` | Free prompt history / project profile / instructions / provider profile |
| `W` / `D` / `O` | Work with the highlighted CSV or XLSX / export MkDocs / open the latest output folder |
| `?` / `q` | Help / quit |

Press **Enter** or `v` on a highlighted file for a read-only preview of UTF-8 text up to 256 KiB. Use the arrow keys or `j`/`k` to scroll and `q` or Esc to return to the picker. Enter opens directories. Press `e` in the picker or text preview to open a file in the [configured editor](#default-editor). Saving applies the edit locally. Press `W` or `e` on a spreadsheet to open its grid and save a separate copy.

Click **Open output folder** in a terminal that supports OSC 8 file links (some terminals use Ctrl+click). Recent archive and job names also link to their folders. Press uppercase `O` to open the latest successful output in the system file manager, or the configured outputs directory before the first save.

### Prompt the selected files

![Composing a prompt for the selected files](images/prompting.png)

### While the model works

![Pipkin running a submitted request](images/running.png)

**Review screen:** press `r`, select a delivery with the arrow keys and Enter. The left pane lists changed files; the right pane shows the actual unified Git diff (green additions, red deletions). On smaller terminals, press Enter to open a full-screen diff.

![Reviewing a diff and approving individual changes](images/diffing_approving.png)

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

![Confirming that approved changes should be copied to the working tree](images/copy_confirm.png)

Decisions are stored under each delivery as `decisions.json` and survive closing Pipkin. Rejected files are never copied. Before copying, Pipkin checks **all** destination paths and original SHA-256 hashes; a conflicting local edit stops the entire copy. Pipkin saves previous file contents under the session’s `backups/` directory and preserves existing file permissions. Review with `git diff` and run your tests before committing.

## VSCodium

### File Explorer

![Pipkin commands in the VSCodium File Explorer context menu](images/vscodium_pipkin_fileviewer_context_commands.png)

### In the editor

#### Run Pipkin commands

![Pipkin commands for the open file](images/vs_infile_pipkin_commands.png)

#### Prompt from a selection

![Pipkin commands for a text selection in VSCodium](images/vscodium_pipkin_selection_context_commands.png)

## Licence

This source code is licensed under the Mozilla Public License 2.0. See [LICENSE](LICENSE).
