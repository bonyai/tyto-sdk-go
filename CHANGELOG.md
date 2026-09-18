# Changelog

All notable changes to `github.com/bonyai/tyto-go` are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-09-18

Initial release. The public surface documented in the README is stable within
`1.x`.

### Added

- **Sandboxes** — create (with an optional display name and optional
  `template`, which defaults to the deployment's configured default template
  (`bonya-dev`) if omitted), get by id or name, list with state and name
  filters, delete, and resume. `SandboxSummary.CreatedAt` reports the creation
  time returned by `ListSandboxes`.
- **Exec** — buffered and streaming, with TTY support and streaming stdin.
- **Managed sessions** — named TTY sessions that outlive the client connection,
  survive suspend/resume, and replay bounded output on reattach.
  `AttachOptions.IdleTimeout` lets callers bound an attachment by client
  inactivity without terminating the managed session; successful stdin writes
  and terminal resizes reset the timer, guest output does not.
- **Filesystem** — read, write, upload, download, list, stat, mkdir, remove,
  and move.
- **Previews** — publish a guest port at an HTTPS URL, in token or public mode,
  with a single-use browser entry point for token mode.
- **Snapshots** — create from a running sandbox, and delete.
- **Jobs** — `RunJob`, `StartJob`, `GetJobRun`, `ListJobRuns`, `CancelJobRun`,
  and job schedules (`CreateJobSchedule`, `GetJobSchedule`,
  `ListJobSchedules`, `UpdateJobSchedule`, `SetJobSchedulePaused`,
  `TriggerJobSchedule`, `DeleteJobSchedule`) — managed, optionally scheduled
  runs of a command or script on a new or existing sandbox. New error types
  `*JobRunNotFoundError` and `*JobScheduleNotFoundError`.
- **Templates** — `ListTemplates` lists the deployment's template catalog.
- **Organization context** — per-client selection of which organization a call
  acts in, defaulting to the caller's personal organization.
- Every operation is a flat method directly on `*Client` or `*Sandbox` — there
  is no collection/namespace type to navigate.
- An `examples/` directory with runnable programs for each capability:
  quickstart, streaming exec, files, managed sessions, previews, and snapshots.
- A `LICENSE` file (MIT), continuous integration, and a `make check` target
  that runs the same checks CI does.

### Notes

- Generated protobuf/gRPC code comes from the Buf Schema Registry as ordinary
  Go module dependencies (`buf.build/gen/go/bonya/tyto/protocolbuffers/go`
  and `.../grpc/go`) instead of being generated locally and vendored into
  `internal/gen`. Building the SDK from a clean checkout needs no `protoc` or
  `protoc-gen-go*` plugins, and the generated code cannot drift from the
  published schema. `internal/gen` was never importable from outside the
  module, and no generated type appears in this SDK's exported API.
- The default endpoint is `https://api.tyto.run`. Set `BONYA_ENDPOINT` to
  point at a self-hosted deployment.
