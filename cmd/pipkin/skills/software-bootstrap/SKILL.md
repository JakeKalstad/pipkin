---
name: software-bootstrap
description: Create a small runnable software project.
---

# Software bootstrap

Turn the goal into a runnable first milestone with an explicit entry point and a smoke check. Choose dependencies and runtime versions from the given constraints. Supply minimal setup and usable example input, with failure and configuration paths documented. Keep credentials in environment variables and use placeholders in examples. Review from a clean checkout for missing dependencies, brittle paths, unexplained defaults, and scope creep.

## Design and build

1. Define the first user-visible scenario and a concrete acceptance check.
2. Specify the smallest architecture that serves that scenario. Choose storage and external dependencies only where the scenario requires them.
3. Wire configuration, an executable entry point, representative input, and clear error output. Include deterministic test fixtures or a smoke check.
4. Record how to install dependencies, configure the project, launch the first scenario, and run checks. Keep example secrets as placeholders.
5. Rehearse the clean-checkout path using the delivered instructions. Check what happens when a required setting, dependency, or service is unavailable.
