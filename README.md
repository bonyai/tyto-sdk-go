# Tyto Go SDK

Run code in a fast, isolated sandbox — from Go.

[![Go Reference](https://pkg.go.dev/badge/github.com/bonyai/tyto-go.svg)](https://pkg.go.dev/github.com/bonyai/tyto-go)
[![Go Version](https://img.shields.io/badge/go-1.24%2B-00ADD8)](go.mod)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

```bash
go get github.com/bonyai/tyto-go
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	tyto "github.com/bonyai/tyto-go"
)

func main() {
	client, err := tyto.NewClient() // reads BONYA_API_KEY
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()
	sandbox, err := client.Sandboxes.Create(ctx, "ubuntu-24.04")
	if err != nil {
		log.Fatal(err)
	}
	defer sandbox.Delete(ctx)

	result, err := sandbox.Exec(ctx, []string{"echo", "hello"}, tyto.ExecOptions{Check: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(result.Stdout()) // hello
}
```

That is a real VM: it boots in about a second, runs anything Linux runs, and is
gone when `Delete` runs.

Module path `github.com/bonyai/tyto-go`, package name `tyto`. The public surface
documented here is stable within `1.x`.

## Contents

- [Install](#install)
- [Configuration](#configuration)
- [What you can do](#what-you-can-do)
- [Create sandboxes](#create-sandboxes)
- [Get and list](#get-and-list)
- [Delete and cleanup](#delete-and-cleanup)
- [Resume](#resume)
- [Buffered exec](#buffered-exec)
- [Streaming exec](#streaming-exec)
- [TTY exec](#tty-exec)
- [Managed console sessions](#managed-console-sessions)
- [Files](#files)
- [Preview URLs](#preview-urls)
- [Snapshots](#snapshots)
- [Error model](#error-model)
- [Troubleshooting](#troubleshooting)
- [Examples](#examples)
- [Development](#development)

## What you can do

| I want to… | Call |
| --- | --- |
| Start a sandbox | `client.Sandboxes.Create(ctx, template)` |
| Reconnect to one | `client.Sandboxes.Get(ctx, id)` / `.GetByName(ctx, name)` |
| Find my sandboxes | `client.Sandboxes.List(ctx)` |
| Run a command | `sandbox.Exec(ctx, cmd)` |
| Watch output as it happens | `sandbox.ExecStream(ctx, cmd)` |
| Keep a terminal alive across reconnects | `sandbox.Sessions.Create(...)` / `.Attach(...)` |
| Read and write files | `sandbox.Files.Read/Write/Upload/Download/...` |
| Expose a port to a browser | `sandbox.Previews.Create(ctx, port)` |
| Save state for later | `sandbox.Snapshot(ctx)` |
| Pause and resume | suspend is automatic; `sandbox.Resume(ctx)` is explicit |
| See which organizations I belong to | `client.ListOrganizations(ctx)` |
| Act in a specific organization | `client.SetOrganizationID(id)`, or `WithOrganizationID` at construction |

Every sandbox operation on `client.Sandboxes` also has a flat form directly on
`Client` — `client.CreateSandbox(ctx, template)`, `client.GetSandbox(ctx, id)`,
`client.ListSandboxes(ctx)`, `client.DeleteSandbox(ctx, id)`,
`client.ResumeSandbox(ctx, id)` — for callers who would rather call a verb than
navigate a namespace. Both spellings are the same implementation; use
whichever reads better at the call site.

Sessions, previews, and snapshots have flat forms too —
`client.CreateSession(ctx, sandboxID, name, cmd)`,
`client.ListSessions(ctx, sandboxID)`, `client.KillSession(ctx, sandboxID, name)`,
`client.AttachSession(ctx, sandboxID, name)`, `client.CreatePreview(ctx, sandboxID, port)`,
`client.ListPreviews(ctx, sandboxID)`, `client.DeletePreview(ctx, sandboxID, id)`,
`client.CreateSnapshot(ctx, sandboxID)`, `client.DeleteSnapshot(ctx, sandboxID, snapshotID)`
— but unlike the sandbox-collection methods above, each of these needs a
resolved `*Sandbox` to call through, so every one does a `GetSandbox` first and
then delegates: one extra round trip compared to already holding the handle.
Prefer `sandbox.Sessions.Create(...)` (or the equivalent) when a `*Sandbox` is
already in hand, such as right after `CreateSandbox`; reach for the flat form
when all you have is an id.

## Install

```bash
go get github.com/bonyai/tyto-go
```

Requires Go 1.24 or newer, and depends on `google.golang.org/grpc` and
`google.golang.org/protobuf`.

## Configuration

Every setting has an environment-variable fallback, so the common case needs no
options at all:

```bash
export BONYA_API_KEY=byk_...
```

```go
client, err := tyto.NewClient()
```

| Option | Environment variable | Default |
| --- | --- | --- |
| `WithAPIKey` | `BONYA_API_KEY` | *required* |
| `WithEndpoint` | `BONYA_ENDPOINT` | `https://api.tyto.run` |
| `WithOrganizationID` | `BONYA_ORGANIZATION_ID` | your personal organization |
| `WithCABundle` | `BONYA_CA_BUNDLE` | system trust store |
| `WithTimeout` | — | `30 * time.Second` |
| `WithMaxRetries` | — | `2` |
| `WithFilesystemReadLimit` | — | 64 MiB |

```go
client, err := tyto.NewClient(
	tyto.WithAPIKey(os.Getenv("BONYA_API_KEY")),
	tyto.WithEndpoint("https://api.tyto.run"),
	tyto.WithOrganizationID("org_123"),
	tyto.WithTimeout(30*time.Second),
)
```

`WithAPIKey` (or `BONYA_API_KEY`) is required.

`WithEndpoint` (or `BONYA_ENDPOINT`) must be an HTTPS URL. The SDK rejects
non-HTTPS URLs, URLs with userinfo, query strings, fragments, malformed ports,
or no host. Trailing slashes are normalized. Point it at your own deployment if
you self-host.

`WithCABundle` (or `BONYA_CA_BUNDLE`) points to a PEM bundle used for private
development CAs. If the file cannot be read, `NewClient` returns an
`*InvalidRequestError`.

`WithTimeout` is the default per-operation deadline. It must be positive.
Buffered and streaming exec calls can override it per call with
`ExecOptions.Timeout`.

`WithMaxRetries` controls SDK retries for retryable control-plane operations. It
must be non-negative. The SDK retries gRPC `UNAVAILABLE` for create, get, list,
delete, resume, snapshot create, and snapshot delete while preserving the same
request and idempotency key where one exists. Exec calls are not retried, except
for one capability refresh when the SDK can prove an exec capability token is
expired before responses start. Filesystem calls are not retried on transport
unavailability; they may refresh a rejected filesystem capability once.

`WithFilesystemReadLimit` caps bytes buffered by `sandbox.Files.Read`. It must
be non-negative and defaults to 64 MiB.

Close clients when done:

```go
client, err := tyto.NewClient()
if err != nil {
	log.Fatal(err)
}
defer client.Close()
```

### Organizations

`WithOrganizationID` selects which organization the client's calls act on. When
omitted, the server resolves the call against your **personal organization** —
the deterministic fallback every account has. An API key belongs to a user, not
to an organization, so one key works across every organization you belong to;
organization ID is how you say which one a given call means.

The REST equivalent is the `X-Bonya-Organization-ID` header. The SDK sends it as
`bonya-organization-id` gRPC metadata on control-plane calls only, injected once
at dial time via a chained unary/stream client interceptor (see
[`transport.go`](transport.go)) rather than at each call site. Exec, filesystem,
and session calls go straight to the sandbox and are authorized by its
capability token, so they carry no organization context.

An empty value is an error rather than a silent fallback:
`WithOrganizationID("")`, or `BONYA_ORGANIZATION_ID` set to an empty string,
makes `NewClient` return an `*InvalidRequestError`. In CI this variable is
usually written as an expansion of another one, and quietly running every job
against someone's personal organization is a worse outcome than failing at
startup.

Naming an organization you do not belong to is a not-found error, identical to
naming one that does not exist.

**In CI, always set it explicitly:**

```yaml
# .github/workflows/integration.yml
env:
  BONYA_API_KEY: ${{ secrets.BONYA_API_KEY }}
  BONYA_ENDPOINT: https://api.tyto.run
  BONYA_ORGANIZATION_ID: ${{ vars.BONYA_ORGANIZATION_ID }}
```

```go
// Both values come from the environment; neither is defaulted away.
client, err := tyto.NewClient()
```

## Create Sandboxes

```go
sandbox, err := client.Sandboxes.Create(ctx, "ubuntu-24.04", tyto.CreateOptions{
	Wait:           tyto.WaitReady,
	IdempotencyKey: "create-job-123",
})
```

`Sandboxes.Create(ctx, template, opts...)` returns a `*Sandbox`.

Parameters:

- `template string` is required and must be non-empty.
- `CreateOptions.Version string` uses the server's default template version
  when empty.
- `CreateOptions.Wait Wait` controls when Create returns. Defaults to
  `WaitReady`.
- `CreateOptions.IdempotencyKey string` is sent to the service. If empty, the
  SDK generates one and reuses it for create transport retries.

Wait modes:

- `WaitReady` asks the service to return a running sandbox. The returned
  handle has `LastObservedStatus == StatusRunning`.
- `WaitNone` returns after the service accepts the request. The returned
  handle has `LastObservedStatus == StatusCreating`.

If create exhausts its deadline, the SDK returns a
`*SandboxCreationTimeoutError`. The error carries the create `IdempotencyKey`
so you can decide whether to retry or inspect server state.

Sandbox fields:

```go
fmt.Println(sandbox.ID)
fmt.Println(sandbox.OperationID)
fmt.Println(sandbox.Template)
fmt.Println(sandbox.Version)
fmt.Println(sandbox.LastObservedStatus)
```

## Get And List

Reconnect to an existing sandbox by ID:

```go
sandbox, err := client.Sandboxes.Get(ctx, "sbx_123")
if err != nil {
	log.Fatal(err)
}
result, err := sandbox.Exec(ctx, "printf reconnected", tyto.ExecOptions{Check: true})
```

`Get(ctx, sandboxID)` requires a non-empty ID and returns a usable `*Sandbox`
handle. It does not explicitly resume the sandbox. Exec and filesystem
operations are the user activity that wakes a suspended sandbox when the
service route supports automatic wake. If a capability is rejected because it
expired, the SDK refreshes the handle with `Get` once before retrying the
operation.

List sandboxes, paging internally:

```go
summaries, err := client.Sandboxes.List(ctx, tyto.ListOptions{
	States: []tyto.Status{tyto.StatusRunning, tyto.StatusSuspended},
	Limit:  20,
})
for _, summary := range summaries {
	fmt.Println(summary.ID, summary.LastObservedStatus)
}
```

`List` returns a `[]SandboxSummary`, paging as needed internally (Go has no
generator protocol as naturally as Python's iterators, so this SDK exposes a
single "collect all matching, up to Limit" call rather than a lazy iterator --
`Limit` bounds how much work `List` does). `Limit: 0` (the default) fetches
every matching sandbox.

Supported state filters are:

- `StatusCreating`
- `StatusRunning`
- `StatusSuspending`
- `StatusSuspended`
- `StatusResuming`
- `StatusFailed`

`StatusDeleted` is not a valid list filter.

`SandboxSummary` contains `ID`, `OperationID`, `Template`, `Version`,
`LastObservedStatus`, `FailureCode`, and `FailureMessage`. Summaries do not
include Exec credentials and cannot run Exec; call `Get(ctx, summary.ID)` for
a usable sandbox handle.

## Delete And Cleanup

```go
result, err := sandbox.Delete(ctx)
fmt.Println(result.SandboxID)
fmt.Println(result.AlreadyDeleted)
```

`sandbox.Delete(ctx)` returns `*DeleteResult{SandboxID, AlreadyDeleted}`.
Calling it again on the same `*Sandbox` is local and idempotent: the second
call returns `AlreadyDeleted: true` without another RPC.

```go
sandbox, err := client.Sandboxes.Create(ctx, "ubuntu-24.04")
if err != nil {
	log.Fatal(err)
}
defer sandbox.Delete(ctx)

if _, err := sandbox.Exec(ctx, "printf work", tyto.ExecOptions{Check: true}); err != nil {
	log.Fatal(err)
}
```

## Resume

Use `Resume` when you want to explicitly resume before running work:

```go
resume, err := sandbox.Resume(ctx, tyto.ResumeOptions{IdempotencyKey: "resume-job-123"})
fmt.Println(resume.SandboxID)
fmt.Println(resume.LifecycleOperationID)
fmt.Println(resume.AlreadyRunning)

result, err := sandbox.Exec(ctx, []string{"printf", "running\n"}, tyto.ExecOptions{Check: true})
```

`sandbox.Resume(ctx, opts...)` returns `*ResumeResult`. It updates the
sandbox's private Exec endpoint and capability when the service returns fresh
values, and sets `LastObservedStatus` to `StatusRunning`.

`IdempotencyKey` is optional. If empty, the SDK generates one and reuses it
for resume transport retries. On ambiguous connection failure, the returned
error carries the idempotency key and the sandbox's local status/capability
are left unchanged.

`Resume` on a failed sandbox returns `*SandboxFailedError` locally before an
RPC.

Automatic wake is different from explicit resume: `Get` does not call
`Resume`, and ordinary Exec/filesystem calls do not make a public
`ResumeSandbox` RPC from the SDK. They use the sandbox's guest endpoint; the
service may wake the sandbox behind that route.

## Buffered Exec

Use `Exec` for commands with bounded output:

```go
result, err := sandbox.Exec(ctx, []string{"python3", "-c", "import os; print(os.environ['MODE'])"}, tyto.ExecOptions{
	Env:     map[string]string{"MODE": "development"},
	Cwd:     "/workspace",
	Timeout: 10 * time.Second,
})

fmt.Println(result.Stdout())
fmt.Println(result.Stderr())
fmt.Println(result.ExitCode)
fmt.Println(result.OK())
```

Signature:

```go
func (s *Sandbox) Exec(ctx context.Context, command any, opts ...ExecOptions) (*ExecResult, error)
```

`command` accepts either:

- `string`: executed as `["/bin/sh", "-c", command]`; the string must be
  non-empty.
- `[]string`: executed directly; the slice must be non-empty and cannot
  contain empty strings.

`ExecOptions.Env` overlays string environment variables. Keys must be
non-empty and cannot contain `=` or NUL. Values must not contain NUL.

`ExecOptions.Cwd` sets the remote working directory. It must be non-empty and
without NUL when set. When empty, the service uses its default working
directory.

`ExecOptions.Input` is written to stdin, then stdin is half-closed before
output is collected. It requires `TTY: false`.

`Exec` returns `*ExecResult`:

```go
result.StdoutBytes    // []byte
result.StderrBytes    // []byte
result.Stdout()        // UTF-8 text with replacement for invalid bytes
result.Stderr()        // UTF-8 text with replacement for invalid bytes
result.ExitCode        // int
result.Signaled        // bool
result.Signal          // int
result.OK()             // ExitCode == 0 && !Signaled
result.String()        // result.Stdout()
```

`ExecOptions.Check: true` calls `result.Check()` before returning. If the
command exits non-zero or by signal, `Exec` returns an `*ExecFailedError`; the
original result is available as `error.Result`.

```go
result, err := sandbox.Exec(ctx, []string{"false"}, tyto.ExecOptions{Check: true})
var execErr *tyto.ExecFailedError
if errors.As(err, &execErr) {
	fmt.Println(execErr.Result.ExitCode)
}
```

`Exec` buffers stdout and stderr in client memory. Use `ExecStream` for large
output, long-running commands, interactive stdin, or cancellation.

## Streaming Exec

Use `ExecStream` when you need events as they arrive:

```go
session, err := sandbox.ExecStream(ctx, []string{"bash", "-lc", "echo out; echo err >&2"})
if err != nil {
	log.Fatal(err)
}
defer session.Close()

for {
	event, err := session.Next()
	if err != nil {
		log.Fatal(err)
	}
	if event == nil {
		break // stream ended cleanly after Exit
	}
	switch e := event.(type) {
	case tyto.Stdout:
		fmt.Print("stdout: ", string(e.Data))
	case tyto.Stderr:
		fmt.Print("stderr: ", string(e.Data))
	case tyto.Exit:
		fmt.Println("exit:", e.ExitCode)
	}
}
```

Signature:

```go
func (s *Sandbox) ExecStream(ctx context.Context, command any, opts ...ExecOptions) (*ExecSession, error)
```

`command`, `Env`, `Cwd`, `TTY`, `Cols`, `Rows`, and `Timeout` follow the same
rules as buffered Exec. `ExecStream` returns an `*ExecSession`; call `Next`
repeatedly to receive `Stdout`, `Stderr`, and `Exit` events until it returns
`(nil, nil)`.

Write streaming stdin as bytes:

```go
session, err := sandbox.ExecStream(ctx, []string{"cat"})
if err != nil {
	log.Fatal(err)
}
defer session.Close()

session.Write([]byte("hello\n"))
session.CloseStdin()

for {
	event, err := session.Next()
	if err != nil || event == nil {
		break
	}
	if stdout, ok := event.(tyto.Stdout); ok {
		fmt.Print(string(stdout.Data))
	}
}
```

`session.Write(data)` accepts a byte slice and returns `*InvalidRequestError`
after the session or stdin is closed. `session.CloseStdin()` is idempotent.
`session.Cancel()` is idempotent and sends a cancel frame when possible.
`session.Close()` cancels an unfinished session -- call it via `defer` for
cleanup.

Internally the SDK replaces Python's queue+thread+condvar machinery with a
background reader goroutine feeding a buffered event channel per underlying
gRPC stream (see `session.go`); this is the biggest Python-to-Go idiom
translation in the SDK.

If `Next` reaches the session deadline before receiving the next event, the
SDK cancels the remote Exec and returns `*TimeoutError`.

## TTY Exec

Set `TTY: true` for terminal semantics:

```go
result, err := sandbox.Exec(ctx, []string{"bash", "-lc", "stty size; printf done"}, tyto.ExecOptions{TTY: true, Check: true})
fmt.Println(result.Stdout())
```

In TTY mode stdout and stderr share the terminal stream. The SDK returns
terminal output as stdout and leaves stderr empty. Streaming TTY sessions emit
`Stdout` events for terminal output; they do not emit separate `Stderr`
events.

Default TTY dimensions are 80 columns by 24 rows. On the wire the SDK sends
`cols=0, rows=0` when both are omitted; the guest runtime interprets that pair
as 80x24.

Provide explicit dimensions by setting both `Cols` and `Rows`:

```go
session, err := sandbox.ExecStream(ctx, []string{"bash"}, tyto.ExecOptions{TTY: true, Cols: 120, Rows: 40})
session.Write([]byte("printf 'ready\\n'\n"))
session.Resize(100, 30)
session.CloseStdin()
```

TTY rules:

- `Cols` and `Rows` must be provided together.
- Each dimension must be an integer from 1 through 512.
- Dimensions require `TTY: true`.
- Buffered `Input` is not allowed with `TTY: true`; use `ExecStream` and
  `session.Write(...)`.
- `session.Resize(cols, rows)` requires a TTY session, open stdin, and an
  unfinished session.

## Managed Console Sessions

Every `*Sandbox` has `sandbox.Sessions`, a `*SandboxSessions` for named,
persistent command sessions that outlive the client connection. This is
different from `ExecStream`: an Exec process dies when its stream closes, but
a managed session keeps running detached, and you can reattach later -- even
after the sandbox warm-suspends and resumes -- and replay what it produced
while nobody was watching.

```go
info, err := sandbox.Sessions.Create(ctx, "server", []string{"bash"}, tyto.CreateSessionOptions{Cols: 120, Rows: 40})
fmt.Println(info.Name, info.Status)

stream, err := sandbox.Sessions.Attach(ctx, "server")
stream.Write([]byte("npm run dev\n"))
stream.Resize(140, 45)
for {
	event, err := stream.Next()
	if err != nil || event == nil {
		break
	}
}
stream.Detach()

result, err := sandbox.Sessions.List(ctx)
for _, info := range result.Sessions {
	fmt.Println(info.Name, info.Status)
}

sandbox.Sessions.Kill(ctx, "server")
```

### Create

```go
func (s *SandboxSessions) Create(ctx context.Context, name string, command []string, opts ...CreateSessionOptions) (SessionInfo, error)
```

`name` must match `^[a-z][a-z0-9-]{0,31}$`. `command` is a non-empty sequence
of non-empty strings -- there is no shell-string convenience like buffered
`Exec`'s. `Cols`/`Rows` are `0` (server default) or an integer from `1`
through `512`.

Creating over an existing record returns `*SessionExistsError` unless
`Replace: true`, and even then only a terminal record (exited, killed, or
failed) is replaced. A running or attached session is never replaced by
`Create`; kill it first.

### List

```go
result, err := sandbox.Sessions.List(ctx)
for _, info := range result.Sessions {
	fmt.Println(info.Name, info.Status)
}
fmt.Println(result.SandboxSuspended)
```

`Sessions.List` returns a `SessionList{Sessions []SessionInfo,
SandboxSuspended bool}`. Listing works on a suspended sandbox without waking
it; `SandboxSuspended: true` marks a result served from the suspend-time
snapshot rather than the live guest.

### Attach

```go
stream, err := sandbox.Sessions.Attach(ctx, "server", tyto.AttachOptions{Cols: 120, Rows: 40})
fmt.Println(stream.Info.Name, stream.ReplayedBytes, stream.HistoryDropped)
```

`Attach(ctx, name, opts...)` returns a `*SessionStream`. `stream.Info`,
`stream.ReplayedBytes`, and `stream.HistoryDropped` are populated immediately
when `Attach` returns, before you call `Next` at all: they describe the
bounded replay the session accumulated while detached. `ReplayedBytes > 0`
means output produced while nobody was attached is being replayed now;
`HistoryDropped: true` means the 1 MiB replay ring dropped some of the oldest
of it. Attaching to a suspended sandbox's session wakes it, the same way
`ExecStream` does.

Attaching preempts any other attached client for that session: the previous
stream receives a `SessionEnded{Reason: SessionEndedReasonTakeover}` event and
ends.

`stream.Next()` yields:

- `Stdout{Data []byte}`: merged output. Sessions are TTY-only, so there is no
  separate stderr stream.
- `Exit{ExitCode, Signaled, Signal}`: the process exited.
- `SessionEnded{Reason}`: the attach ended without the process exiting --
  `SessionEndedReasonDetached` (you called `Detach`) or
  `SessionEndedReasonTakeover` (another client attached instead).
- `SessionOutputDropped{DroppedBytes}`: live output was dropped because the
  client was reading too slowly. This does not end the attach.

`stream.Write(data)` sends stdin. `stream.Resize(cols, rows)` takes an integer
from `1` through `512` for each dimension. `stream.Detach()` ends the attach
gracefully without touching the process. `stream.Close()` calls `Detach` if
the stream is still open -- call it via `defer`.

### Kill

```go
sandbox.Sessions.Kill(ctx, "server", tyto.KillOptions{Signal: "TERM", GraceMS: 5000})
```

Signals the session's process group (default `TERM`), escalating to
`SIGKILL` after `GraceMS` if it has not exited. Returns a `SessionInfo`, but
exit info is not guaranteed on that specific response: `Kill` signals and
returns without waiting for the guest to reap the process, so a `List` shortly
afterward is the reliable way to observe the final exit code. Killing an
unknown name returns `*SessionNotFoundError`.

### SessionInfo

```go
info.Name              // string
info.Command            // []string
info.WorkingDir         // string
info.Status             // SessionStatus
info.Attached           // bool
info.StartedAt          // time.Time
info.LastActivityAt     // time.Time
info.EndedAt            // time.Time (zero value while running)
info.Exit                // *Exit, non-nil only once terminal
```

`SessionStatus` values are `SessionStatusUnspecified`, `SessionStatusStarting`,
`SessionStatusIdle`, `SessionStatusAttached`, `SessionStatusExited`,
`SessionStatusKilled`, and `SessionStatusFailed`.

### Capability refresh

Session calls transparently reissue an expired capability and retry once, the
same way `ExecStream` and `sandbox.Files` do. Call `sandbox.ReissueCapability`
directly only if you manage tokens yourself.

## Files

Every `*Sandbox` has `sandbox.Files`, a `*SandboxFiles`:

```go
sandbox.Files.Write(ctx, "/workspace/message.txt", []byte("hello\n"))
payload, err := sandbox.Files.Read(ctx, "/workspace/message.txt")

sandbox.Files.Upload(ctx, "local-input.bin", "/workspace/input.bin")
sandbox.Files.Download(ctx, "/workspace/input.bin", "local-output.bin")

entries, err := sandbox.Files.List(ctx, "/workspace")
info, err := sandbox.Files.Stat(ctx, "/workspace/message.txt")

sandbox.Files.Mkdir(ctx, "/workspace/output")
sandbox.Files.Move(ctx, "/workspace/message.txt", "/workspace/output/message.txt")
sandbox.Files.Remove(ctx, "/workspace/output", true) // recursive
```

Methods:

- `Read(ctx, path) ([]byte, error)`
- `Write(ctx, path, data []byte) error`
- `Upload(ctx, localPath, remotePath string) error`
- `Download(ctx, remotePath, localPath string) error`
- `List(ctx, path) ([]FileInfo, error)`
- `Stat(ctx, path) (FileInfo, error)`
- `Mkdir(ctx, path) error`
- `Remove(ctx, path string, recursive bool) error`
- `Move(ctx, source, destination string) error`

Remote paths must be non-empty strings without NUL. The SDK accepts absolute
or relative remote paths and leaves interpretation to the guest runtime.

`Read` buffers the entire remote file in memory and returns bytes. It returns
`*FilesystemLimitError` before exceeding `filesystem_read_limit`.

`Write` streams the payload in 64 KiB chunks, writes through a guest-side
temporary file, and publishes it by replacing the final directory entry. The
final path is not followed when it is a symlink.

`Upload` streams a local file to the remote path in 64 KiB chunks. `Download`
streams a remote file into a hidden temporary file in the destination
directory, fsyncs it, atomically replaces the destination with `os.Rename`,
and fsyncs the parent directory. If a read or write error happens before
replacement, the temporary file is removed and the previous destination is
left unchanged.

`List` returns immediate children sorted by name. It returns a complete slice
or an error; it does not return partial results after a remote listing error.

`Stat` returns lstat-style metadata. A final symlink is reported as a symlink
rather than followed.

`Move` is same-filesystem, atomic, and no-overwrite. Cross-filesystem moves
return `*CrossFilesystemMoveError`; destination-exists errors return
`*RemoteFileExistsError`.

`Remove(ctx, path, true)` removes directories recursively. Recursive remove
does not follow symlinks and is not atomic.

`FileInfo` fields:

```go
info, err := sandbox.Files.Stat(ctx, "/workspace/output/message.txt")
fmt.Println(info.Path)
fmt.Println(info.Name)
fmt.Println(info.Kind == tyto.FileKindFile)
fmt.Println(info.Size)
fmt.Printf("%o\n", info.Mode)
fmt.Println(info.ModifiedAt) // time.Time
```

`FileKind` values are `FileKindFile`, `FileKindDirectory`, `FileKindSymlink`,
and `FileKindOther`.

## Preview URLs

A preview publishes one guest port at an HTTPS URL a browser can open. The
server must bind a port in 1024-65535; privileged ports are never previewable.

```go
preview, err := sandbox.Previews.Create(ctx, 3000, tyto.CreatePreviewOptions{Name: "web"})
fmt.Println(preview.URL) // https://pv-<26 chars>.preview.tyto.run

previews, err := sandbox.Previews.List(ctx)
err = sandbox.Previews.Delete(ctx, preview.ID)
```

### Opening one in a browser

A token-mode preview needs the sandbox's capability, and a URL is not a safe
place to leave one. `BrowserURL` produces a single-use entry point: the
gateway validates the token, trades it for a host-scoped `HttpOnly` cookie,
and redirects to the same address without it, so no page is ever rendered at a
URL containing the credential.

```go
url, err := sandbox.Previews.BrowserURL(preview)
// open url in a browser
```

Open it once and let the cookie carry the session. **Do not share that URL**
-- anyone who receives it holds the sandbox's data-plane capability until it
expires. It errors on a public preview, which has no token to exchange.

### Public previews

```go
public, err := sandbox.Previews.Create(ctx, 8080, tyto.CreatePreviewOptions{Auth: tyto.PreviewAuthPublic})
```

`PreviewAuthPublic` means exactly that: anyone with the URL reaches the
service, with no credential. `PreviewAuthToken` is the default and an omitted
`Auth` never yields a public URL.

### Capability upgrade

`Create` returns a fresh capability and the SDK stores it on the sandbox
automatically, because the preview scope is newer than the token a sandbox is
created with. If you are holding a capability elsewhere, refresh it
explicitly with `sandbox.ReissueCapability(ctx)`.

### Suspend and wake

Traffic to a preview URL wakes a suspended sandbox and the request is served
once it is running. See the Python SDK README's Preview URLs section for the
full set of edge-case notes (SSE reconnect, WebSocket auth, suspend cutting
open connections) -- these are server/gateway behaviors, identical for every
SDK.

## Snapshots

```go
snapshot, err := sandbox.Snapshot(ctx, tyto.SnapshotOptions{IdempotencyKey: "snapshot-job-123"})
fmt.Println(snapshot.ID)
fmt.Println(snapshot.SourceSandboxID)

snapshot.Delete(ctx)
snapshot.Delete(ctx) // local no-op
```

`sandbox.Snapshot(ctx, opts...)` returns `*Snapshot`. If `IdempotencyKey` is
empty, the SDK generates one and reuses it for snapshot create transport
retries. Using the same key for the same source sandbox returns the same
snapshot identity when the service accepts idempotent replay.

Snapshot create requires a running source sandbox. Locally deleted or
observed-deleted sandboxes return `*SandboxDeletedError`; failed sandboxes
return `*SandboxFailedError`; suspended sandboxes return
`*SandboxSuspendedError`.

`snapshot.Delete(ctx)` returns `nil` and is idempotent on the same `*Snapshot`.
Snapshots can be deleted after deleting the source sandbox handle.

## Organizations

An api key belongs to a user, not a single organization, so one key works
across every organization that user belongs to. Calls are scoped to whichever
organization is current on the client.

```go
organizations, err := client.ListOrganizations(ctx)
for _, org := range organizations {
    fmt.Println(org.ID, org.Name, org.Personal, org.Role)
}

err = client.SetOrganizationID(organizations[0].ID)
```

`ListOrganizations(ctx)` returns every organization the caller belongs to,
including their personal organization. `Organization.Personal` marks that
one — it's the deterministic tenant an omitted organization context resolves
to, and every account has exactly one. TApi stores its name as the literal
string `"personal"`; render that however fits your UI rather than showing it
verbatim.

`SetOrganizationID(id)` changes which organization subsequent calls run
against, including calls on a client that is already connected — it takes
effect immediately, not just on the next dial. An empty id is rejected as an
`*InvalidRequestError` rather than silently falling back to the personal
organization. To set it once at construction instead, use
`tyto.WithOrganizationID(id)`; `OrganizationID()` reads back whatever is
currently in effect.

## Error Model

All SDK errors embed `BaseError` and expose `Message()`, `SandboxID`,
`OperationID`, and `IdempotencyKey`. Use `errors.As` to match a specific type:

```go
sandbox, err := client.Sandboxes.Get(ctx, "sbx_missing")
var notFound *tyto.SandboxNotFoundError
if errors.As(err, &notFound) {
	fmt.Println("sandbox does not exist or is not visible")
}
```

Public error types:

- `*AuthenticationError`: invalid or rejected API key.
- `*InvalidRequestError`: invalid local arguments or invalid service response.
- `*SandboxNotFoundError`: sandbox missing, deleted, or not visible to the API
  key.
- `*SandboxDeletedError`: operation cannot run because the sandbox is deleted.
- `*SandboxSuspendedError`: operation reported a suspended sandbox.
- `*SandboxBusyError`: service rejected a lifecycle operation as busy.
- `*SandboxFailedError`: operation cannot run because the sandbox failed.
- `*SandboxCreationFailedError`: create reached a failed terminal state.
- `*SandboxCreationTimeoutError`: create deadline expired.
- `*CapabilityRejectedError`: guest capability was rejected and could not be
  refreshed.
- `*SessionExistsError`: `Sessions.Create` targeted a name that already has a
  record and either `Replace: true` was not given or the record is not
  terminal.
- `*SessionNotFoundError`: `Sessions.Attach` or `Sessions.Kill` named a
  session that does not exist.
- `*FilesystemError`: general filesystem failure.
- `*RemoteFileNotFoundError`: remote file or directory missing (embeds
  `FilesystemError`; `errors.As(err, &fsErr)` also matches).
- `*RemoteFileExistsError`: remote destination already exists.
- `*CrossFilesystemMoveError`: remote move crosses filesystems.
- `*FilesystemLimitError`: client or service filesystem size/frame limit.
- `*ExecFailedError`: `ExecResult.Check()` or `ExecOptions.Check: true` saw a
  non-OK result; carries `Result *ExecResult`.
- `*TimeoutError`: operation deadline expired.
- `*ConnectionError`: retryable transport failure exhausted retries.
- `*ServiceError`: service or unexpected transport failure not covered above.

The SDK redacts API keys, capabilities, selected operation identifiers
supplied to the error mapper, and path-like internal details from mapped
service messages.

Examples:

```go
_, err := client.Sandboxes.Get(ctx, "sbx_123")
var authErr *tyto.AuthenticationError
var notFoundErr *tyto.SandboxNotFoundError
switch {
case errors.As(err, &authErr):
	fmt.Println("check BONYA_API_KEY")
case errors.As(err, &notFoundErr):
	fmt.Println("sandbox does not exist or is not visible")
}
```

```go
_, err := sandbox.Files.Read(ctx, "/workspace/missing.txt")
var notFound *tyto.RemoteFileNotFoundError
var fsErr *tyto.FilesystemError
switch {
case errors.As(err, &notFound):
	fmt.Println("missing")
case errors.As(err, &fsErr):
	fmt.Println("filesystem failed:", fsErr.Message())
}
```

```go
_, err := sandbox.Exec(ctx, []string{"sleep", "60"}, tyto.ExecOptions{Timeout: time.Second})
var timeoutErr *tyto.TimeoutError
if errors.As(err, &timeoutErr) {
	fmt.Println("command timed out and was cancelled")
}
```

## Resource Ownership

Go has no context-manager protocol, so use `defer` for deterministic cleanup:

```go
client, err := tyto.NewClient(tyto.WithAPIKey("BONYA_API_KEY"), tyto.WithEndpoint("https://api.tyto.run"))
if err != nil {
	log.Fatal(err)
}
defer client.Close()

sandbox, err := client.Sandboxes.Create(ctx, "ubuntu-24.04")
if err != nil {
	log.Fatal(err)
}
defer sandbox.Delete(ctx)

session, err := sandbox.ExecStream(ctx, []string{"cat"})
if err != nil {
	log.Fatal(err)
}
defer session.Close()
session.Write([]byte("hello\n"))
session.CloseStdin()
```

Ownership rules:

- `Client.Close()` closes cached channels and is idempotent.
- `sandbox.Delete(ctx)` affects the remote sandbox and updates the local
  handle to `StatusDeleted`; calling it again is a local no-op.
- `session.Close()` (on an `*ExecSession`) cancels an unfinished session.
- `stream.Close()` (on a `*SessionStream`) detaches an unfinished attach
  rather than killing the guest process, which keeps running.
- `snapshot.Delete(ctx)` deletes the remote snapshot identity and is a local
  no-op when repeated on the same object.

For intentionally persistent sandboxes, do not `defer sandbox.Delete(ctx)`.
Store `sandbox.ID`, close the client, and reconnect later with
`client.Sandboxes.Get`.

## Current Limitations

This Go SDK intentionally matches the Python SDK's scope, adapted to Go
idiom:

- There is no public `Suspend()` method.
- There are no public networking, fork, template-engine, or multi-host APIs.
- Managed sessions are TTY-only; there is no non-TTY managed session mode.
- There is no `sandbox.Console()` attach-or-create convenience, and no
  multi-attach or collaborative terminal mode -- a new attach always preempts
  the previous one.
- `SandboxSummary` values are metadata only and cannot run Exec.
- Buffered Exec stores stdout and stderr in memory.
- `sandbox.Files.Read()` stores the full file in memory up to
  `filesystem_read_limit`.
- Filesystem writes, uploads, moves, mkdir, and removes are not retried after
  ambiguous transport errors.
- Remote filesystem path normalization, permissions, symlink traversal inside
  parent directories, and service-side file size limits are guest-runtime
  behavior, not SDK behavior.
- `Sandboxes.List` collects into a single `[]SandboxSummary` bounded by
  `Limit` rather than exposing a lazy iterator; it still pages internally.

## Troubleshooting

**`*InvalidRequestError: api_key is required`**
Nothing supplied a key. Set `BONYA_API_KEY`, or pass `tyto.WithAPIKey(...)`. If
you use the `tyto` CLI, `tyto login` saves a key — but to a config file the SDK
does not read, so export it:

```bash
export BONYA_API_KEY=byk_...
```

**`*AuthenticationError`**
The key reached the server and was rejected. It may be revoked, or belong to a
different deployment than `BONYA_ENDPOINT` points at.

**`*InvalidRequestError: endpoint must use https`**
The endpoint is validated before any connection is attempted. `http://` URLs,
bare hostnames, and URLs carrying userinfo, a query string, or a fragment are
all rejected. `https://api.tyto.run` is the shape to match.

**`*InvalidRequestError: organization_id must be a non-empty string`**
`BONYA_ORGANIZATION_ID` is set but empty — usually an unset variable expanded in
CI. This is deliberately an error rather than a fallback to your personal
organization; see [Organizations](#organizations).

**`*SandboxNotFoundError` on a sandbox you just created**
Most often an organization mismatch: the sandbox was created in one organization
and looked up in another. Sandboxes are not visible across organizations, and a
sandbox in an organization you cannot see is reported the same way as one that
does not exist.

**`*SandboxCreationTimeoutError`**
Create did not reach a running state before the deadline. The error carries the
`IdempotencyKey` it used — retry `Create` with that same key to join the
original creation rather than starting a second sandbox.

**TLS/certificate errors against a private deployment**
Point `tyto.WithCABundle(...)` (or `BONYA_CA_BUNDLE`) at the PEM bundle for your
CA.

**`*FilesystemLimitError` from `Files.Read`**
The file is larger than the filesystem read limit (64 MiB by default). Raise it
with `tyto.WithFilesystemReadLimit(...)`, or use `Files.Download`, which streams
to disk instead of buffering.

**A command hangs**
`Exec` buffers all output and returns only when the process exits, so a server
or REPL never returns. Use `ExecStream`, or run it as a
[managed session](#managed-console-sessions).

**`GetByName` returns an error for a name that exists**
Names are not unique. When more than one sandbox carries the name, `GetByName`
reports an error rather than picking one, because silently choosing would make a
later `Delete` destroy an arbitrary sandbox. Use `List` with `ListOptions{Name:
...}` and decide yourself — that is what the `tyto` CLI does when it prompts.

## Examples

Runnable programs are in [`examples/`](examples), a separate module so `go get`
on this SDK never pulls them in:

| Directory | Shows |
| --- | --- |
| [`quickstart`](examples/quickstart) | Create, exec, clean up |
| [`exec-streaming`](examples/exec-streaming) | Streaming output and stdin |
| [`files`](examples/files) | Read, write, upload, download, list |
| [`sessions`](examples/sessions) | Persistent terminals and replay |
| [`previews`](examples/previews) | Publishing a port to a browser |
| [`snapshots`](examples/snapshots) | Capturing sandbox state |

```bash
export BONYA_API_KEY=byk_...
cd examples && go run ./quickstart
```

## Development

```bash
make check      # vet + test + build the examples module, the same checks CI runs
make test
make vet
```

### Protobuf/gRPC code

There is no local codegen step and no `protoc` toolchain to install. The
generated code is consumed straight from the Buf Schema Registry as two
ordinary Go modules:

| Module | Package | Contains |
| --- | --- | --- |
| `buf.build/gen/go/bonya/tyto/protocolbuffers/go` | `runtimev1` | message types |
| `buf.build/gen/go/bonya/tyto/grpc/go` | `runtimev1grpc` | service clients/servers |

To pick up a new schema, [publish it](../../bsr/README.md) and bump both:

```bash
go get buf.build/gen/go/bonya/tyto/protocolbuffers/go@latest \
       buf.build/gen/go/bonya/tyto/grpc/go@latest
```

BSR generates these on demand, so a just-published version can briefly be
missing from `proxy.golang.org`'s cache. If `go get` keeps resolving the
previous version, fetch through the BSR proxy directly:

```bash
GOPROXY=https://buf.build/gen/go,https://proxy.golang.org,direct GOSUMDB=off \
  go get buf.build/gen/go/bonya/tyto/protocolbuffers/go@latest
```

## See also

- [Python SDK](../python) · [TypeScript SDK](../typescript)
- [`tyto` CLI](../../cli) — the same API from a terminal, built on this SDK
