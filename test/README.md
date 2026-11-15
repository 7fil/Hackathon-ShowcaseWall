# Tests

Every automated test of this project lives in this folder. Nothing under `internal/`, `pkg/`,
`middleware/` or `cmd/` contains a `_test.go` file. `test/layout` fails the run if one appears.

## Layout

The folders mirror the code they cover.

| Folder | Covers |
| :--- | :--- |
| `test/admin` | `internal/admin/service`: role checks, publish / unpublish / feature, cache and event order, paging |
| `test/adminseed` | `internal/adminseed`: promoting accounts to Admin from the CLI |
| `test/media/service` | `internal/media/service`: image checks, object keys, signed and stable URLs, cover and screenshot streaming |
| `test/media/repository` | `internal/media/repository`: local-disk storage, and the cover lifecycle on a real disk |
| `test/works` | `internal/works/service`: screenshot ownership, limits, public detail |
| `test/realtime` | `internal/realtime/service`: WebSocket origin check, rate limit, per-IP limit, slow-client drop |
| `test/config` | `pkg/config`: environment validation per `APP_ENV` |
| `test/logger` | `pkg/logger`: JSON / text output, secret redaction, email masking |
| `test/metrics` | `pkg/metrics`: Prometheus exposition and pre-registered series |
| `test/pgarray` | `pkg/pgarray`: Postgres array round trip |
| `test/middleware` | `middleware`: request-ID handling |
| `test/layout` | the rule above: no `_test.go` outside `test/` |

## Running

```bash
go test ./test/...            # everything
go test -race ./test/...      # with the race detector (what the README recommends)
go test ./test/works          # one area
```

## Conventions

- **External test packages.** Each folder is its own package named `<folder>_test` and imports the
  code under test like any other caller. Tests can therefore only use exported APIs. If a rule is
  hard to test that way, test it through a public entry point (for example, signature expiry is
  checked with `StreamCover` and the `Deps.Now` clock hook) rather than exporting internals.
- **One package per folder.** Helpers such as `fakeRepo` or `newSvc` are local to their folder, so
  the same names can be reused across areas without clashing.
- **No new test files elsewhere.** Add new tests to the matching folder here, or create a new folder
  that mirrors the package you are covering.
