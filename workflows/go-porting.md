# Go Porting Record

The repository migration from Python helper files to Go is complete. Some shell
tests use inline Python for test data and assertions.

This record keeps the method for a future language port. It does not describe
planned Workcell work.

## Preserved method

1. Record the old CLI, file, permission, and error contracts.
2. Add differential tests that use the same fixtures.
3. Port low-coupling tools before shared policy code.
4. Put shared parser and validation code in one library.
5. Change callers only after parity tests pass.
6. Remove the old runtime and validation code last.

Each port must preserve these properties:

- CLI flags and exit codes.
- JSON and TOML shapes.
- Deterministic order.
- Secret file modes.
- Path checks and symlink rejection.
- Mutation resistance for security decisions.

Use the Go standard library by default. A new dependency needs a written
supply-chain rationale.

The validation entry point is:

```sh
./scripts/go-port-validate.sh
```

Do not use this record as evidence for a new port. Add fresh parity tests and
validation for the new source and destination languages.
