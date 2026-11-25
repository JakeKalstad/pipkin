---
name: software-maintenance
description: Make and review a change to existing software.
---

# Software maintenance

Read the repository's instructions, entry points, callers, and relevant tests. Locate the smallest change that fixes the behavior through its normal entry point. Preserve public contracts, permissions, data created by earlier versions, and compatibility with operations that retry or fail halfway. For changes to authentication or validation, review boundaries and defensive regression coverage. Check migrations across existing data and new installations. Use targeted checks first and report unavailable dependencies or unverified paths.

## Diagnose and implement

- Reproduce the behavior from supplied evidence or an available test. Trace input validation, business logic, persistence, and the user-visible outcome through the ordinary entry point.
- Identify the compatibility contract before updating an API, schema, serialization format, or configuration default. Consider migrations, mixed-version callers, and recovery from interruption wherever relevant.
- For asynchronous or retried operations, identify the durable state, transitions, idempotency key, timeout, and failure path. Check whether concurrent workers can violate the invariant.
- Add targeted regression coverage when the requested change warrants it. Exercise both the outcome and an important failure or compatibility case. Respect existing test and formatting conventions.

## Review and release

Check the full diff for accidental scope changes, credentials, risky defaults, and unchecked operations. Verify migration and rollback assumptions using the actual release process when supplied. Identify any deployment-dependent check that cannot be performed from the available workspace.
