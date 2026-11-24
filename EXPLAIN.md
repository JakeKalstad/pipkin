# Pipkin Code Project Explanation

Welcome! This document explains the Pipkin code project in a way that's friendly for junior developers. Pipkin is a tool designed to help you package, prompt, and review code changes using AI models.

---

## 1. Environment Loading (`env.go`)

- **Purpose:** Loads environment variables from `.env` files without overwriting existing environment variables.
- **How it works:**
  - Reads lines from a `.env` file.
  - Ignores comments and empty lines.
  - Parses lines as `KEY=value` pairs.
  - Validates keys to ensure they are valid environment variable names.
  - Sets environment variables only if they don't already exist.

This helps configure the app with environment variables safely.

---

## 2. Langfuse Reporting (`langfuse.go`)

- **Purpose:** Sends metadata-only trace information to Langfuse, a tracing service.
- **Key points:**
  - Sends trace data about AI job executions (like model used, tokens consumed).
  - Uses OTLP/HTTP protocol.
  - Requires Langfuse public and secret keys from environment variables.
  - Does not send sensitive data like prompts or API keys.
  - Fails silently so it doesn't affect the main workflow.

---

## 3. Main Application (`main.go`)

- **Purpose:** The core CLI and TUI (terminal UI) application logic.

### Key Components:

- **Config:** Holds settings like provider, API keys, model, and endpoints.

- **Node & App:** Represent files and directories in the project tree, with selection and expansion states.

- **Ignore Rules:** Defines which files and directories to ignore (e.g., `.git`, `node_modules`, `.env`).

- **File Scanning:** Recursively scans directories to build a tree of files and folders, respecting ignore rules.

- **ZIP Creation:** Packages selected files into ZIP archives.

- **User Input Handling:** Reads keyboard input for navigation and commands in the TUI.

- **Sending Requests:** Sends selected files and prompts to AI providers (OpenAI or multipart endpoints) and handles responses.

- **LLM Interaction:** Sends a prompt and project files to an LLM, receives JSON-formatted changes, and applies them to a new ZIP archive.

- **Safety Checks:** Validates file paths and contents to prevent unsafe operations or path traversal.

- **Modes:** Supports multiple modes like MCP server, web UI, ZIP creation, workflow submission, and review.

- **Terminal UI:** Renders a file tree, status messages, and prompt input in the terminal.

---

## 4. Workflow Management (`workflow.go`)

- **Purpose:** Handles the lifecycle of a coding job, including packaging, sending to AI, receiving results, and reviewing.

- **WorkflowJob:** Struct that tracks job metadata, status, tokens used, and file hashes.

- **ZIP Validation:** Ensures ZIP files are safe, not empty, and don't contain unsafe paths.

- **Sending to Providers:** Supports different providers:
  - `openai-archive`: Uploads ZIP to OpenAI's file API and uses the code interpreter tool.
  - `multipart`: Sends ZIP and prompt as multipart form data.
  - `openai`: Sends files and prompt to OpenAI chat completions.

- **Reviewing Changes:** Compares input and output ZIPs, extracts diffs, and prepares files for review.

- **Applying Changes:** Applies approved changes to the local working tree safely, with backups and conflict detection.

- **Git Diff:** Shows diffs between original and modified files using `git diff --no-index`.

---

## 5. Review UI (`review_tui.go`)

- **Purpose:** Provides a terminal-based interface to review changes returned by the AI.

- **Features:**
  - Lists changed files with statuses.
  - Shows diffs with color highlighting.
  - Allows approving, rejecting, or resetting decisions per file.
  - Supports copying approved changes to the working tree.
  - Saves decisions persistently.

---

## 6. Web UI (`web.go` and `web.html`)

- **Purpose:** Provides a local web interface for selecting files, submitting prompts, and reviewing changes.

- **Security:** Only binds to loopback addresses to prevent remote access.

- **CSRF Protection:** Uses tokens to protect POST requests.

- **Features:**
  - File tree with selection checkboxes.
  - Prompt input.
  - List of recent deliveries.
  - Review pages with diffs and decision buttons.

---

## 7. MCP Server (`mcp.go`)

- **Purpose:** Implements a JSON-RPC server over stdio for integration with other tools.

- **Tools:** Supports commands like packing files, submitting jobs, listing jobs, reviewing, and applying changes.

- **Security:** Validates paths and disables applying changes by default.

---

## 8. Utilities and Helpers

- **Path Safety:** Functions to ensure paths are relative, safe, and do not contain symlinks.

- **Terminal Handling:** Uses `stty` to set raw mode for terminal input.

- **Spinner and Animations:** Visual feedback during long operations.

- **File Ignoring:** Rules for ignoring common build, dependency, and secret files.

---

## Summary

Pipkin is a sophisticated tool that helps you package your code, send it with instructions to AI models, receive suggested changes, and review/apply those changes safely. It supports multiple interfaces (CLI, TUI, web, MCP) and providers, with strong safety and usability features.

If you're new, focus on understanding how files are scanned and selected, how ZIP archives are created, and how the workflow sends and receives data from AI providers. The review UI helps you inspect changes before applying them.

Happy coding! 🎉
