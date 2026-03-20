package store

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
	"time"
)

func TestMemoryStoreObjectMetadataAndListOrdering(t *testing.T) {
	s := NewMemoryStore()
	modTime := time.Date(2026, 3, 20, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))

	body := []byte("payload-z")
	obj := s.PutLoadedObject("bench", "z-key", body, modTime)
	body[0] = 'X'
	s.PutLoadedObject("bench", "a-key", []byte("payload-a"), modTime)

	if got, want := string(obj.Body), "payload-z"; got != want {
		t.Fatalf("stored body = %q, want %q", got, want)
	}
	sum := md5.Sum([]byte("payload-z"))
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`
	if got := obj.ETag; got != wantETag {
		t.Fatalf("etag = %q, want %q", got, wantETag)
	}
	if got, want := obj.LastModified, modTime.UTC(); !got.Equal(want) {
		t.Fatalf("last modified = %v, want %v", got, want)
	}

	listed, ok := s.ListObjects("bench", "")
	if !ok {
		t.Fatal("ListObjects returned missing bucket")
	}
	if len(listed) != 2 {
		t.Fatalf("listed len = %d, want 2", len(listed))
	}
	if got, want := listed[0].Key, "a-key"; got != want {
		t.Fatalf("listed[0].key = %q, want %q", got, want)
	}
	if got, want := listed[1].Key, "z-key"; got != want {
		t.Fatalf("listed[1].key = %q, want %q", got, want)
	}

	prefixed, ok := s.ListObjects("bench", "z-")
	if !ok {
		t.Fatal("ListObjects prefix returned missing bucket")
	}
	if len(prefixed) != 1 || prefixed[0].Key != "z-key" {
		t.Fatalf("prefixed list = %#v, want only z-key", prefixed)
	}
}

func TestMemoryStoreDeleteObjectAndBucket(t *testing.T) {
	s := NewMemoryStore()
	s.CreateBucket("alpha")
	s.PutLoadedObject("alpha", "one", []byte("1"), time.Time{})
	s.CreateBucket("beta")

	if got := s.ListBuckets(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("ListBuckets = %#v, want [alpha beta]", got)
	}
	if !s.BucketExists("alpha") {
		t.Fatal("expected alpha bucket to exist")
	}
	if !s.DeleteObject("alpha", "one") {
		t.Fatal("DeleteObject(alpha, one) = false, want true")
	}
	if _, ok := s.GetObject("alpha", "one"); ok {
		t.Fatal("GetObject(alpha, one) = found, want missing")
	}
	if !s.DeleteBucket("beta") {
		t.Fatal("DeleteBucket(beta) = false, want true")
	}
	if s.DeleteBucket("beta") {
		t.Fatal("DeleteBucket(beta) second call = true, want false")
	}
}
