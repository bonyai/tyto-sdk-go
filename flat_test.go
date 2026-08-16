package tyto

import (
	"context"
	"testing"
)

// TestFlatSandboxMethodsDelegateToTheCollection proves the two spellings
// reach the same sandbox: a flat CreateSandbox followed by the namespaced
// Sandboxes.Get (and vice versa for the other operations) must agree,
// because both are backed by exactly one implementation.
func TestFlatSandboxMethodsDelegateToTheCollection(t *testing.T) {
	fake := &fakeTApi{createdName: "flat-test"}
	client := newBufconnClient(t, fake)
	ctx := context.Background()

	created, err := client.CreateSandbox(ctx, "ubuntu-24.04")
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	viaNamespace, err := client.Sandboxes.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Sandboxes.Get: %v", err)
	}
	if viaNamespace.ID != created.ID {
		t.Fatalf("Sandboxes.Get(%q).ID = %q, want %q", created.ID, viaNamespace.ID, created.ID)
	}

	viaFlat, err := client.GetSandbox(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if viaFlat.ID != created.ID {
		t.Fatalf("GetSandbox(%q).ID = %q, want %q", created.ID, viaFlat.ID, created.ID)
	}

	byName, err := client.GetSandboxByName(ctx, "flat-test")
	if err != nil {
		t.Fatalf("GetSandboxByName: %v", err)
	}
	if byName.ID != created.ID {
		t.Fatalf("GetSandboxByName.ID = %q, want %q", byName.ID, created.ID)
	}

	summaries, err := client.ListSandboxes(ctx)
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != created.ID {
		t.Fatalf("ListSandboxes = %+v, want one summary for %q", summaries, created.ID)
	}
}

func TestFlatDeleteSandboxDoesNotRequireAHandle(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	result, err := client.DeleteSandbox(context.Background(), "sbx-flat-delete")
	if err != nil {
		t.Fatalf("DeleteSandbox: %v", err)
	}
	if result.SandboxID != "sbx-flat-delete" {
		t.Fatalf("SandboxID = %q, want %q", result.SandboxID, "sbx-flat-delete")
	}
}

func TestFlatResumeSandboxDoesNotRequireAHandle(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)

	result, err := client.ResumeSandbox(context.Background(), "sbx-flat-resume")
	if err != nil {
		t.Fatalf("ResumeSandbox: %v", err)
	}
	if result.SandboxID != "sbx-flat-resume" {
		t.Fatalf("SandboxID = %q, want %q", result.SandboxID, "sbx-flat-resume")
	}
}

// TestSandboxResumeStillRefreshesTheHandle guards the refactor that made
// Sandbox.Resume delegate to SandboxCollection.resumeSandbox: the shared
// implementation must still hand back the raw response so the handle-aware
// caller can update its own capability and exec endpoint, which ResumeResult
// itself does not carry.
func TestSandboxResumeStillRefreshesTheHandle(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)
	ctx := context.Background()

	sandbox, err := client.Sandboxes.Create(ctx, "ubuntu-24.04")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sandbox.LastObservedStatus = StatusSuspended

	if _, err := sandbox.Resume(ctx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if sandbox.LastObservedStatus != StatusRunning {
		t.Fatalf("LastObservedStatus after Resume = %v, want %v", sandbox.LastObservedStatus, StatusRunning)
	}
}

// TestSandboxDeleteStillShortCircuitsLocally guards the refactor that made
// Sandbox.Delete delegate to SandboxCollection.Delete: a second call on the
// same handle must remain a local no-op, since only the handle -- not the
// collection-level flat form -- can know it already deleted this sandbox.
func TestSandboxDeleteStillShortCircuitsLocally(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake)
	ctx := context.Background()

	sandbox, err := client.Sandboxes.Create(ctx, "ubuntu-24.04")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := sandbox.Delete(ctx); err != nil {
		t.Fatalf("Delete (first): %v", err)
	}
	result, err := sandbox.Delete(ctx)
	if err != nil {
		t.Fatalf("Delete (second): %v", err)
	}
	if !result.AlreadyDeleted {
		t.Fatal("second Delete on the same handle must report AlreadyDeleted without another RPC")
	}
}
