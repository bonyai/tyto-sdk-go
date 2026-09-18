package tyto

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// fakeFile is one entry in fakeGuest's in-memory filesystem.
type fakeFile struct {
	data                []byte
	isDir               bool
	mode                uint32
	modifiedAtUnixNanos int64
}

// withFiles adds an in-memory filesystem to fakeGuest, so file RPCs (which
// fakeGuest otherwise leaves at UnimplementedGuestServiceServer's default
// "unimplemented" response) can be exercised against a real bidi/streaming
// gRPC surface, not a mock.
func (f *fakeGuest) withFiles(files map[string]fakeFile) *fakeGuest {
	f.files = files
	return f
}

func (f *fakeGuest) ReadFile(req *runtimev1.ReadFileRequest, stream grpc.ServerStreamingServer[runtimev1.ReadFileResponse]) error {
	file, ok := f.files[req.GetPath()]
	if !ok || file.isDir {
		return status.Error(codes.NotFound, "file not found")
	}
	const chunk = 8
	data := file.data
	if len(data) == 0 {
		return nil
	}
	for offset := 0; offset < len(data); offset += chunk {
		end := min(offset+chunk, len(data))
		if err := stream.Send(&runtimev1.ReadFileResponse{Data: data[offset:end]}); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeGuest) WriteFile(stream grpc.ClientStreamingServer[runtimev1.WriteFileRequest, runtimev1.WriteFileResponse]) error {
	var path string
	var data []byte
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch frame := req.GetFrame().(type) {
		case *runtimev1.WriteFileRequest_Start:
			path = frame.Start.GetPath()
		case *runtimev1.WriteFileRequest_Chunk:
			data = append(data, frame.Chunk.GetData()...)
		}
	}
	if f.files == nil {
		f.files = map[string]fakeFile{}
	}
	f.files[path] = fakeFile{data: data, mode: 0o644}
	return stream.SendAndClose(&runtimev1.WriteFileResponse{BytesWritten: uint64(len(data))})
}

func (f *fakeGuest) ListDirectory(req *runtimev1.ListDirectoryRequest, stream grpc.ServerStreamingServer[runtimev1.ListDirectoryResponse]) error {
	for path, file := range f.files {
		dir := filepath.Dir(path)
		if dir != filepath.Clean(req.GetPath()) {
			continue
		}
		kind := runtimev1.FileKind_FILE_KIND_FILE
		if file.isDir {
			kind = runtimev1.FileKind_FILE_KIND_DIRECTORY
		}
		info := &runtimev1.FileInfo{
			Path:                path,
			Name:                filepath.Base(path),
			Kind:                kind,
			Size:                uint64(len(file.data)),
			Mode:                file.mode,
			ModifiedAtUnixNanos: file.modifiedAtUnixNanos,
		}
		if err := stream.Send(&runtimev1.ListDirectoryResponse{File: info}); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeGuest) StatFile(ctx context.Context, req *runtimev1.StatFileRequest) (*runtimev1.StatFileResponse, error) {
	file, ok := f.files[req.GetPath()]
	if !ok {
		return nil, status.Error(codes.NotFound, "file not found")
	}
	kind := runtimev1.FileKind_FILE_KIND_FILE
	if file.isDir {
		kind = runtimev1.FileKind_FILE_KIND_DIRECTORY
	}
	return &runtimev1.StatFileResponse{File: &runtimev1.FileInfo{
		Path:                req.GetPath(),
		Name:                filepath.Base(req.GetPath()),
		Kind:                kind,
		Size:                uint64(len(file.data)),
		Mode:                file.mode,
		ModifiedAtUnixNanos: file.modifiedAtUnixNanos,
	}}, nil
}

func (f *fakeGuest) MakeDirectory(ctx context.Context, req *runtimev1.MakeDirectoryRequest) (*runtimev1.MakeDirectoryResponse, error) {
	if f.files == nil {
		f.files = map[string]fakeFile{}
	}
	f.files[req.GetPath()] = fakeFile{isDir: true}
	return &runtimev1.MakeDirectoryResponse{}, nil
}

func (f *fakeGuest) RemoveFile(ctx context.Context, req *runtimev1.RemoveFileRequest) (*runtimev1.RemoveFileResponse, error) {
	if _, ok := f.files[req.GetPath()]; !ok {
		return nil, status.Error(codes.NotFound, "file not found")
	}
	delete(f.files, req.GetPath())
	return &runtimev1.RemoveFileResponse{}, nil
}

func (f *fakeGuest) MoveFile(ctx context.Context, req *runtimev1.MoveFileRequest) (*runtimev1.MoveFileResponse, error) {
	file, ok := f.files[req.GetSourcePath()]
	if !ok {
		return nil, status.Error(codes.NotFound, "file not found")
	}
	if _, exists := f.files[req.GetDestinationPath()]; exists {
		return nil, status.Error(codes.AlreadyExists, "destination exists")
	}
	delete(f.files, req.GetSourcePath())
	f.files[req.GetDestinationPath()] = file
	return &runtimev1.MoveFileResponse{}, nil
}

func TestReadFileReturnsContent(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{
		"/workspace/greeting.txt": {data: []byte("hello world")},
	}))
	data, err := sandbox.ReadFile(context.Background(), "/workspace/greeting.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("ReadFile() = %q, want %q", data, "hello world")
	}
}

func TestReadFileNotFound(t *testing.T) {
	sandbox := newBufconnSandbox(t, &fakeGuest{})
	_, err := sandbox.ReadFile(context.Background(), "/workspace/missing.txt")
	var notFound *RemoteFileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("ReadFile error = %v, want *RemoteFileNotFoundError", err)
	}
}

func TestWriteFileThenReadFileRoundTrips(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{}))
	if err := sandbox.WriteFile(context.Background(), "/workspace/out.txt", []byte("captured")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := sandbox.ReadFile(context.Background(), "/workspace/out.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "captured" {
		t.Errorf("ReadFile() = %q, want %q", data, "captured")
	}
}

func TestStatFile(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{
		"/workspace/greeting.txt": {data: []byte("hello"), mode: 0o644},
	}))
	info, err := sandbox.StatFile(context.Background(), "/workspace/greeting.txt")
	if err != nil {
		t.Fatalf("StatFile: %v", err)
	}
	if info.Size != 5 || info.Kind != FileKindFile {
		t.Errorf("StatFile() = %+v", info)
	}
}

func TestListFiles(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{
		"/workspace/a.txt": {data: []byte("a")},
		"/workspace/b.txt": {data: []byte("bb")},
	}))
	entries, err := sandbox.ListFiles(context.Background(), "/workspace")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "a.txt" || entries[1].Name != "b.txt" {
		t.Errorf("ListFiles() = %+v, want a.txt then b.txt", entries)
	}
}

func TestMkdirFile(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{}))
	if err := sandbox.MkdirFile(context.Background(), "/workspace/output"); err != nil {
		t.Fatalf("MkdirFile: %v", err)
	}
	info, err := sandbox.StatFile(context.Background(), "/workspace/output")
	if err != nil {
		t.Fatalf("StatFile: %v", err)
	}
	if info.Kind != FileKindDirectory {
		t.Errorf("StatFile().Kind = %v, want directory", info.Kind)
	}
}

func TestMoveFile(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{
		"/workspace/a.txt": {data: []byte("hi")},
	}))
	if err := sandbox.MoveFile(context.Background(), "/workspace/a.txt", "/workspace/b.txt"); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if _, err := sandbox.StatFile(context.Background(), "/workspace/a.txt"); err == nil {
		t.Error("source still exists after MoveFile")
	}
	data, err := sandbox.ReadFile(context.Background(), "/workspace/b.txt")
	if err != nil || string(data) != "hi" {
		t.Errorf("ReadFile(dest) = (%q, %v)", data, err)
	}
}

func TestRemoveFile(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{
		"/workspace/a.txt": {data: []byte("hi")},
	}))
	if err := sandbox.RemoveFile(context.Background(), "/workspace/a.txt", false); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	if _, err := sandbox.StatFile(context.Background(), "/workspace/a.txt"); err == nil {
		t.Error("file still exists after RemoveFile")
	}
}

func TestUploadFileAndDownloadFileRoundTrip(t *testing.T) {
	sandbox := newBufconnSandbox(t, (&fakeGuest{}).withFiles(map[string]fakeFile{}))

	localSrc := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(localSrc, []byte("upload me"), 0o644); err != nil {
		t.Fatalf("write local src: %v", err)
	}

	if err := sandbox.UploadFile(context.Background(), localSrc, "/workspace/uploaded.txt"); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	localDst := filepath.Join(t.TempDir(), "dst.txt")
	if err := sandbox.DownloadFile(context.Background(), "/workspace/uploaded.txt", localDst); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	got, err := os.ReadFile(localDst)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "upload me" {
		t.Errorf("downloaded content = %q, want %q", got, "upload me")
	}
}
