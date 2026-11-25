# Project instructions

## Understand the task and the supplied context

- Identify the expected behaviour and the entry points, callers, and tests affected by the request.
- Follow the project's existing patterns. Use conventions and commands found in the supplied README, build files, CI configuration, and tests.
- If a missing file or requirement blocks a reliable answer, name what you need and explain why. Keep assumptions explicit when you can proceed.

## Make focused changes

- Reuse existing helpers and interfaces. Preserve compatibility for callers and saved data unless the task explicitly changes it.
- Keep edits within the requested scope. Update tests, API or MCP schemas, and usage documentation when their behaviour changes.
- Handle failure paths and retries where relevant. Keep credentials out of source, examples, and output.

## Verify and review

- Run the smallest relevant check documented by the project when the environment allows it.
- Trace user-facing changes through their normal entry points. Review the patch for regressions, incomplete wiring, and unrelated changes.
- Report the outcome, the checks actually run and their results, and anything that still needs verification.
