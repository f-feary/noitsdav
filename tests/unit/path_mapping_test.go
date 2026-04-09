package unit

import (
	"testing"

	"noitsdav/internal/mounts"
)

func TestResolveRoot(t *testing.T) {
	t.Parallel()
	got, err := mounts.Resolve("/")
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsRoot {
		t.Fatal("expected root")
	}
}

func TestResolveMountPath(t *testing.T) {
	t.Parallel()
	got, err := mounts.Resolve("/media/folder/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	if got.MountName != "media" || got.BackendPath != "/folder/file.bin" {
		t.Fatalf("unexpected resolve result: %+v", got)
	}
}

