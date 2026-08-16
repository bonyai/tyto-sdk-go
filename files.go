package tyto

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	runtimev1 "github.com/bonyai/tyto-go/internal/gen/tyto/runtime/v1"
)

// transferChunkBytes is the chunk size used for streamed writes/uploads.
const transferChunkBytes = 64 * 1024

// SandboxFiles is the dedicated sandbox filesystem RPC surface. Read buffers
// subject to the client's memory cap. Upload and Download stream in 64 KiB
// chunks without a total transfer cap.
type SandboxFiles struct {
	sandbox *Sandbox
}

// Read buffers an entire remote file and returns its bytes. It errors with
// *FilesystemLimitError before exceeding the client's filesystem read limit.
func (f *SandboxFiles) Read(ctx context.Context, path string) ([]byte, error) {
	path, err := validateRemotePath(path)
	if err != nil {
		return nil, err
	}
	var result []byte
	err = f.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, err := f.guestClient()
		if err != nil {
			return err
		}
		callCtx, cancel, err := f.callContext(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		stream, err := client.ReadFile(callCtx, &runtimev1.ReadFileRequest{SandboxId: f.sandbox.ID, Path: path})
		if err != nil {
			return f.mapError(err)
		}
		var data []byte
		limit := f.sandbox.client.filesystemReadLimit
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return f.mapError(err)
			}
			chunk := resp.GetData()
			if int64(len(data)+len(chunk)) > limit {
				return &FilesystemLimitError{FilesystemError{BaseError{
					Msg:         "filesystem read exceeded client memory limit",
					SandboxID:   f.sandbox.ID,
					OperationID: f.sandbox.OperationID,
				}}}
			}
			data = append(data, chunk...)
		}
		result = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Write writes data to a remote path, streamed in 64 KiB chunks through a
// guest-side temporary file and published atomically.
func (f *SandboxFiles) Write(ctx context.Context, path string, data []byte) error {
	path, err := validateRemotePath(path)
	if err != nil {
		return err
	}
	return f.writeStream(ctx, path, func(send func([]byte) error) error {
		for offset := 0; offset < len(data); offset += transferChunkBytes {
			end := min(offset+transferChunkBytes, len(data))
			if err := send(data[offset:end]); err != nil {
				return err
			}
		}
		return nil
	})
}

// Upload streams a local file to the remote path in 64 KiB chunks.
func (f *SandboxFiles) Upload(ctx context.Context, localPath, remotePath string) error {
	remotePath, err := validateRemotePath(remotePath)
	if err != nil {
		return err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return &InvalidRequestError{BaseError{Msg: "local file could not be read: " + err.Error()}}
	}
	defer file.Close()
	return f.writeStream(ctx, remotePath, func(send func([]byte) error) error {
		buf := make([]byte, transferChunkBytes)
		for {
			n, readErr := file.Read(buf)
			if n > 0 {
				if err := send(buf[:n]); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return &InvalidRequestError{BaseError{Msg: "local file could not be read: " + readErr.Error()}}
			}
		}
	})
}

// Download streams a remote file into a hidden temporary file in the
// destination directory, fsyncs it, and atomically replaces the destination.
func (f *SandboxFiles) Download(ctx context.Context, remotePath, localPath string) error {
	remotePath, err := validateRemotePath(remotePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(localPath)
	base := filepath.Base(localPath)
	tmp, err := os.CreateTemp(dir, "."+base+".bonya-download-*.tmp")
	if err != nil {
		return &InvalidRequestError{BaseError{Msg: "temporary download file could not be created: " + err.Error()}}
	}
	tmpPath := tmp.Name()
	replaced := false
	defer func() {
		if !replaced {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	err = f.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, err := f.guestClient()
		if err != nil {
			return err
		}
		callCtx, cancel, err := f.callContext(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		stream, err := client.ReadFile(callCtx, &runtimev1.ReadFileRequest{SandboxId: f.sandbox.ID, Path: remotePath})
		if err != nil {
			return f.mapError(err)
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return &InvalidRequestError{BaseError{Msg: err.Error()}}
		}
		if err := tmp.Truncate(0); err != nil {
			return &InvalidRequestError{BaseError{Msg: err.Error()}}
		}
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return f.mapError(err)
			}
			if _, err := tmp.Write(resp.GetData()); err != nil {
				return &InvalidRequestError{BaseError{Msg: "temporary download file could not be written: " + err.Error()}}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := tmp.Sync(); err != nil {
		return &InvalidRequestError{BaseError{Msg: "temporary download file could not be synced: " + err.Error()}}
	}
	if err := tmp.Close(); err != nil {
		return &InvalidRequestError{BaseError{Msg: err.Error()}}
	}
	if err := os.Rename(tmpPath, localPath); err != nil {
		return &InvalidRequestError{BaseError{Msg: "download could not be finalized: " + err.Error()}}
	}
	replaced = true
	fsyncParentDir(dir)
	return nil
}

// List returns immediate children of a remote directory, sorted by name.
func (f *SandboxFiles) List(ctx context.Context, path string) ([]FileInfo, error) {
	path, err := validateRemotePath(path)
	if err != nil {
		return nil, err
	}
	var result []FileInfo
	err = f.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, err := f.guestClient()
		if err != nil {
			return err
		}
		callCtx, cancel, err := f.callContext(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		stream, err := client.ListDirectory(callCtx, &runtimev1.ListDirectoryRequest{SandboxId: f.sandbox.ID, Path: path})
		if err != nil {
			return f.mapError(err)
		}
		var files []FileInfo
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return f.mapError(err)
			}
			file := resp.GetFile()
			if file == nil {
				return &InvalidRequestError{BaseError{Msg: "ListDirectory response is missing file metadata"}}
			}
			files = append(files, fileInfoFromProto(file))
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		result = files
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Stat returns lstat-style metadata for a remote path.
func (f *SandboxFiles) Stat(ctx context.Context, path string) (FileInfo, error) {
	path, err := validateRemotePath(path)
	if err != nil {
		return FileInfo{}, err
	}
	var result FileInfo
	err = f.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, err := f.guestClient()
		if err != nil {
			return err
		}
		callCtx, cancel, err := f.callContext(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		resp, err := client.StatFile(callCtx, &runtimev1.StatFileRequest{SandboxId: f.sandbox.ID, Path: path})
		if err != nil {
			return f.mapError(err)
		}
		file := resp.GetFile()
		if file == nil {
			return &InvalidRequestError{BaseError{Msg: "StatFile response is missing file metadata"}}
		}
		result = fileInfoFromProto(file)
		return nil
	})
	if err != nil {
		return FileInfo{}, err
	}
	return result, nil
}

// Mkdir creates a remote directory.
func (f *SandboxFiles) Mkdir(ctx context.Context, path string) error {
	path, err := validateRemotePath(path)
	if err != nil {
		return err
	}
	return f.unaryMutation(ctx, func(ctx context.Context, client runtimev1.GuestServiceClient) error {
		_, err := client.MakeDirectory(ctx, &runtimev1.MakeDirectoryRequest{SandboxId: f.sandbox.ID, Path: path})
		return err
	})
}

// Remove removes a remote path, recursively if recursive is true.
func (f *SandboxFiles) Remove(ctx context.Context, path string, recursive bool) error {
	path, err := validateRemotePath(path)
	if err != nil {
		return err
	}
	return f.unaryMutation(ctx, func(ctx context.Context, client runtimev1.GuestServiceClient) error {
		_, err := client.RemoveFile(ctx, &runtimev1.RemoveFileRequest{SandboxId: f.sandbox.ID, Path: path, Recursive: recursive})
		return err
	})
}

// Move moves a remote file or directory. It is same-filesystem, atomic, and
// no-overwrite.
func (f *SandboxFiles) Move(ctx context.Context, source, destination string) error {
	source, err := validateRemotePath(source)
	if err != nil {
		return err
	}
	destination, err = validateRemotePath(destination)
	if err != nil {
		return err
	}
	return f.unaryMutation(ctx, func(ctx context.Context, client runtimev1.GuestServiceClient) error {
		_, err := client.MoveFile(ctx, &runtimev1.MoveFileRequest{SandboxId: f.sandbox.ID, SourcePath: source, DestinationPath: destination})
		return err
	})
}

func (f *SandboxFiles) writeStream(ctx context.Context, path string, produce func(send func([]byte) error) error) error {
	return f.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, err := f.guestClient()
		if err != nil {
			return err
		}
		callCtx, cancel, err := f.callContext(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		stream, err := client.WriteFile(callCtx)
		if err != nil {
			return f.mapError(err)
		}
		if err := stream.Send(&runtimev1.WriteFileRequest{
			Frame: &runtimev1.WriteFileRequest_Start{Start: &runtimev1.WriteFileStart{SandboxId: f.sandbox.ID, Path: path}},
		}); err != nil {
			return f.mapError(err)
		}
		sendErr := produce(func(chunk []byte) error {
			return stream.Send(&runtimev1.WriteFileRequest{
				Frame: &runtimev1.WriteFileRequest_Chunk{Chunk: &runtimev1.WriteFileChunk{Data: chunk}},
			})
		})
		if sendErr != nil {
			_, _ = stream.CloseAndRecv()
			return f.mapError(sendErr)
		}
		if _, err := stream.CloseAndRecv(); err != nil {
			return f.mapError(err)
		}
		return nil
	})
}

func (f *SandboxFiles) unaryMutation(ctx context.Context, call func(context.Context, runtimev1.GuestServiceClient) error) error {
	return f.withCapabilityRefresh(ctx, func(ctx context.Context) error {
		client, err := f.guestClient()
		if err != nil {
			return err
		}
		callCtx, cancel, err := f.callContext(ctx)
		if err != nil {
			return err
		}
		defer cancel()
		if err := call(callCtx, client); err != nil {
			return f.mapError(err)
		}
		return nil
	})
}

func (f *SandboxFiles) withCapabilityRefresh(ctx context.Context, call func(context.Context) error) error {
	if err := f.ensureFilesAllowed(); err != nil {
		return err
	}
	err := call(ctx)
	var rejected *CapabilityRejectedError
	if err == nil {
		return nil
	}
	if !asCapabilityRejected(err, &rejected) {
		return err
	}
	if refreshErr := f.sandbox.refreshCapabilityOnce(ctx); refreshErr != nil {
		return refreshErr
	}
	return call(ctx)
}

func asCapabilityRejected(err error, target **CapabilityRejectedError) bool {
	if e, ok := err.(*CapabilityRejectedError); ok {
		*target = e
		return true
	}
	return false
}

func (f *SandboxFiles) ensureFilesAllowed() error {
	if f.sandbox.isDeleted() {
		return &SandboxDeletedError{BaseError{Msg: "sandbox has been deleted", SandboxID: f.sandbox.ID, OperationID: f.sandbox.OperationID}}
	}
	if f.sandbox.LastObservedStatus == StatusFailed {
		return f.sandbox.failedError()
	}
	return nil
}

func (f *SandboxFiles) guestClient() (runtimev1.GuestServiceClient, error) {
	execEndpoint, _ := f.sandbox.snapshotState()
	return f.sandbox.client.guestClient(execEndpoint)
}

func (f *SandboxFiles) callContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	remaining, err := startDeadlineRemaining(f.sandbox.client.timeout)
	if err != nil {
		return nil, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	_, capability := f.sandbox.snapshotState()
	callCtx = withOutgoingMetadata(callCtx, "bonya-sandbox-id", f.sandbox.ID, "bonya-exec-capability", capability)
	return callCtx, cancel, nil
}

func (f *SandboxFiles) mapError(err error) error {
	_, capability := f.sandbox.snapshotState()
	mapped := MapRPCError(err, f.sandbox.client.secrets(capability), WithSandboxID(f.sandbox.ID), WithOperationID(f.sandbox.OperationID), WithFilesystemRPC())
	if _, ok := mapped.(*SandboxDeletedError); ok {
		f.sandbox.mu.Lock()
		f.sandbox.deleted = true
		f.sandbox.LastObservedStatus = StatusDeleted
		f.sandbox.mu.Unlock()
	}
	return mapped
}

func startDeadlineRemaining(timeout time.Duration) (time.Duration, error) {
	dl, err := startDeadline(timeout)
	if err != nil {
		return 0, err
	}
	return dl.remaining()
}

func fileInfoFromProto(file *runtimev1.FileInfo) FileInfo {
	return FileInfo{
		Path:       file.GetPath(),
		Name:       file.GetName(),
		Kind:       fileKindFromProto(file.GetKind()),
		Size:       file.GetSize(),
		Mode:       file.GetMode(),
		ModifiedAt: time.Unix(0, file.GetModifiedAtUnixNanos()).UTC(),
	}
}

func fileKindFromProto(kind runtimev1.FileKind) FileKind {
	switch kind {
	case runtimev1.FileKind_FILE_KIND_FILE:
		return FileKindFile
	case runtimev1.FileKind_FILE_KIND_DIRECTORY:
		return FileKindDirectory
	case runtimev1.FileKind_FILE_KIND_SYMLINK:
		return FileKindSymlink
	default:
		return FileKindOther
	}
}

func fsyncParentDir(dir string) {
	if dir == "" {
		dir = "."
	}
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Sync()
}
