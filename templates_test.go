package tyto

import (
	"context"
	"testing"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

func TestListTemplatesMapsFields(t *testing.T) {
	fake := &fakeTApi{templates: []*runtimev1.TApiTemplate{
		{TemplateId: "ubuntu-24.04", Version: "1", Digest: "sha256:aaa", IsDefault: true},
		{TemplateId: "ubuntu-24.04", Version: "2", Digest: "sha256:bbb", IsDefault: false},
	}}
	client := newBufconnClient(t, fake)

	templates, err := client.ListTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("templates = %d, want 2", len(templates))
	}

	first := templates[0]
	if first.ID != "ubuntu-24.04" || first.Version != "1" || first.Digest != "sha256:aaa" || !first.IsDefault {
		t.Errorf("first template = %+v", first)
	}

	second := templates[1]
	if second.ID != "ubuntu-24.04" || second.Version != "2" || second.IsDefault {
		t.Errorf("second template = %+v", second)
	}
}

func TestListTemplatesEmpty(t *testing.T) {
	fake := &fakeTApi{templates: []*runtimev1.TApiTemplate{}}
	client := newBufconnClient(t, fake)

	templates, err := client.ListTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("templates = %v, want empty", templates)
	}
}
