# Pipkin VSCodium extension v0.7

Right-click files/folders to package or send to the configured LLM. Jobs and archives are stored in `~/pipkin/outputs/` (or the configured Pipkin output directory). Review completed sessions through **Pipkin: Review Saved Session**, inspect native diffs, and approve individual files.

CLI users can invoke `pipkin --mcp` for OpenCode/Hermes MCP integration. The extension itself does not need to run an agent.


The **Pipkin** activity-bar view now lists saved sessions. Open a session for native Submitted ↔ Result or Current ↔ Result diffs.

## Licence

This source code is licensed under the Mozilla Public License 2.0. See [LICENSE](LICENSE).

## Delivery options

When sending files, choose **Standard** for one model pass or **Agentic** for a draft and review. Set `pipkin.providerProfile` in editor settings to choose a named Pipkin provider profile for editor requests. Open **Pipkin Deliveries** to revisit the prompt, inspect diffs, or see why an attempt failed.
