# Python-to-Go Migration Record

The repository migration from Python helper files to Go is complete. Some shell
tests use inline Python for test data and assertions.

This page records the completed compatibility method. It is not an active
migration plan.

## Completed sequence

1. The project recorded the Python behavior and security checks.
2. Differential tests compared Python and Go with the same fixtures.
3. Low-coupling tools moved before the policy and injection tools.
4. Shared Go libraries replaced duplicate parser logic and path checks.
5. Callers changed only after their parity tests passed.
6. Validation moved after helper parity.
7. The project removed the residual repo-owned Python helper files.

The sequence covered these helper groups:

- Scenario manifest tools.
- Direct mount extraction.
- PTY transcript tools.
- Policy parser and renderer.
- Credential source resolution.
- Authentication policy management.

## Preserved gates

Each cutover compared these results:

- Exit code.
- Standard output and standard error.
- JSON or TOML output.
- File tree and content.
- File modes.
- Security error paths.

The migration kept shell entry points stable until the Go tools had parity.

For a future language port, create a new plan. Use fresh baseline evidence and
tests for that change.
