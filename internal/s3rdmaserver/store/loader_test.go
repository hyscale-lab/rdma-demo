package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoaderMapsFoldersToBucketsAndFilesToKeys(t *testing.T) {
	root := t.TempDir()

	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta", "nested")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) failed: %v", err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(beta) failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignore-me.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("WriteFile(ignore) failed: %v", err)
	}

	alphaFile := filepath.Join(alphaDir, "part-00000.csv")
	betaFile := filepath.Join(betaDir, "part-00001.csv")
	if err := os.WriteFile(alphaFile, []byte("alpha-body"), 0o644); err != nil {
		t.Fatalf("WriteFile(alpha) failed: %v", err)
	}
	if err := os.WriteFile(betaFile, []byte("beta-body"), 0o644); err != nil {
		t.Fatalf("WriteFile(beta) failed: %v", err)
	}

	alphaTime := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	betaTime := time.Date(2026, 3, 20, 11, 0, 0, 0, time.UTC)
	if err := os.Chtimes(alphaFile, alphaTime, alphaTime); err != nil {
		t.Fatalf("Chtimes(alpha) failed: %v", err)
	}
	if err := os.Chtimes(betaFile, betaTime, betaTime); err != nil {
		t.Fatalf("Chtimes(beta) failed: %v", err)
	}

	s := NewMemoryStore()
	loader := NewLoader(s)
	result, err := loader.LoadRoot(root)
	if err != nil {
		t.Fatalf("LoadRoot failed: %v", err)
	}

	if got, want := result.BucketsLoaded, 2; got != want {
		t.Fatalf("BucketsLoaded = %d, want %d", got, want)
	}
	if got, want := result.ObjectsLoaded, 2; got != want {
		t.Fatalf("ObjectsLoaded = %d, want %d", got, want)
	}
	if got, want := result.BytesLoaded, int64(len("alpha-body")+len("beta-body")); got != want {
		t.Fatalf("BytesLoaded = %d, want %d", got, want)
	}
	if got, want := result.Buckets, []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Buckets = %#v, want %#v", got, want)
	}

	alphaObj, ok := s.GetObject("alpha", "part-00000.csv")
	if !ok {
		t.Fatal("expected alpha object to exist")
	}
	if got, want := string(alphaObj.Body), "alpha-body"; got != want {
		t.Fatalf("alpha body = %q, want %q", got, want)
	}
	if got, want := alphaObj.LastModified, alphaTime.UTC(); !got.Equal(want) {
		t.Fatalf("alpha last modified = %v, want %v", got, want)
	}

	betaObj, ok := s.GetObject("beta", "nested/part-00001.csv")
	if !ok {
		t.Fatal("expected beta nested object to exist")
	}
	if got, want := string(betaObj.Body), "beta-body"; got != want {
		t.Fatalf("beta body = %q, want %q", got, want)
	}
	if got, want := betaObj.LastModified, betaTime.UTC(); !got.Equal(want) {
		t.Fatalf("beta last modified = %v, want %v", got, want)
	}
}

func TestLoaderRejectsInvalidRoot(t *testing.T) {
	s := NewMemoryStore()
	loader := NewLoader(s)

	if _, err := loader.LoadRoot(""); err == nil {
		t.Fatal("LoadRoot(\"\") = nil error, want failure")
	}

	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if _, err := loader.LoadRoot(filePath); err == nil {
		t.Fatal("LoadRoot(file) = nil error, want failure")
	}
}
