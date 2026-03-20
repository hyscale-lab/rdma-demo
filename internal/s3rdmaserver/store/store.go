package store

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// Object is a readable startup-loaded object kept in memory for GET/HEAD/list.
type Object struct {
	Body         []byte
	ETag         string
	LastModified time.Time
}

// ListedObject keeps list results stable and easy to consume.
type ListedObject struct {
	Key    string
	Object Object
}

// MemoryStore is the minimal in-memory state needed by the new benchmark server.
type MemoryStore struct {
	mu      sync.RWMutex
	buckets map[string]map[string]Object
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		buckets: make(map[string]map[string]Object),
	}
}

func (s *MemoryStore) CreateBucket(name string) {
	if strings.TrimSpace(name) == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[name]; !ok {
		s.buckets[name] = make(map[string]Object)
	}
}

func (s *MemoryStore) BucketExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.buckets[name]
	return ok
}

func (s *MemoryStore) DeleteBucket(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[name]; !ok {
		return false
	}
	delete(s.buckets, name)
	return true
}

func (s *MemoryStore) PutLoadedObject(bucket, key string, body []byte, lastModified time.Time) Object {
	obj := Object{
		Body:         append([]byte(nil), body...),
		ETag:         quotedMD5(body),
		LastModified: normalizeModTime(lastModified),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[bucket]
	if !ok {
		b = make(map[string]Object)
		s.buckets[bucket] = b
	}
	b[key] = obj
	return obj
}

func (s *MemoryStore) GetObject(bucket, key string) (Object, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buckets[bucket]
	if !ok {
		return Object{}, false
	}
	obj, ok := b[key]
	return obj, ok
}

func (s *MemoryStore) DeleteObject(bucket, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[bucket]
	if !ok {
		return false
	}
	if _, ok := b[key]; !ok {
		return false
	}
	delete(b, key)
	return true
}

func (s *MemoryStore) ListObjects(bucket, prefix string) ([]ListedObject, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buckets[bucket]
	if !ok {
		return nil, false
	}

	out := make([]ListedObject, 0, len(b))
	for key, obj := range b {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		out = append(out, ListedObject{Key: key, Object: obj})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out, true
}

func (s *MemoryStore) ListBuckets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.buckets))
	for bucket := range s.buckets {
		out = append(out, bucket)
	}
	sort.Strings(out)
	return out
}

func quotedMD5(body []byte) string {
	sum := md5.Sum(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func normalizeModTime(v time.Time) time.Time {
	if v.IsZero() {
		return time.Now().UTC()
	}
	return v.UTC()
}
