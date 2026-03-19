package payload

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rdma-demo/server-client-demo/internal/inmems3/store"
)

func TestLoaderAddFolderLoadsBucketTreeIntoStore(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "nexus-benchmark-payload", "input_payload", "mapper", "part-00000.csv"), "alpha,beta\n1,2\n")
	mustWriteFile(t, filepath.Join(root, "nexus-benchmark-payload", "input_payload", "mapper", "part-00001.csv"), "alpha,beta\n3,4\n")
	mustWriteFile(t, filepath.Join(root, "other-bucket", "top-level.txt"), "payload")

	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	loader := NewLoader(memStore)

	result, err := loader.AddFolder(context.Background(), root)
	if err != nil {
		t.Fatalf("AddFolder: %v", err)
	}

	if got, want := result.BucketsLoaded, 2; got != want {
		t.Fatalf("BucketsLoaded = %d, want %d", got, want)
	}
	if got, want := result.ObjectsLoaded, 3; got != want {
		t.Fatalf("ObjectsLoaded = %d, want %d", got, want)
	}

	obj, ok := memStore.GetObject("nexus-benchmark-payload", "input_payload/mapper/part-00000.csv")
	if !ok {
		t.Fatal("expected loaded object to exist")
	}
	if got, want := string(obj.Body), "alpha,beta\n1,2\n"; got != want {
		t.Fatalf("loaded body = %q, want %q", got, want)
	}
}

func TestLoaderClearAllRemovesLoadedBuckets(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "bench-bucket", "seed-object"), "payload")

	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	loader := NewLoader(memStore)
	if _, err := loader.AddFolder(context.Background(), root); err != nil {
		t.Fatalf("AddFolder: %v", err)
	}

	deleted := loader.ClearAll()
	if got, want := deleted, 1; got != want {
		t.Fatalf("ClearAll deleted = %d, want %d", got, want)
	}
	if buckets := loader.ListBuckets(); len(buckets) != 0 {
		t.Fatalf("expected no buckets after ClearAll, got %v", buckets)
	}
}

func mustWriteFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
