package store

import (
	"container/list"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type EvictPolicy string

const (
	EvictPolicyReject EvictPolicy = "reject"
	EvictPolicyFIFO   EvictPolicy = "fifo"
)

var ErrCapacityExceeded = errors.New("store capacity exceeded")

func ParseEvictPolicy(v string) (EvictPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", string(EvictPolicyReject):
		return EvictPolicyReject, nil
	case string(EvictPolicyFIFO):
		return EvictPolicyFIFO, nil
	default:
		return "", fmt.Errorf("unsupported store eviction policy %q", v)
	}
}

type Object struct {
	Body         []byte
	ETag         string
	LastModified time.Time
}

type ListedObject struct {
	Key    string
	Object Object
}

type MemoryStore struct {
	mu          sync.RWMutex
	buckets     map[string]map[string]Object
	maxBytes    int64
	evictPolicy EvictPolicy
	usedBytes   int64
	fifoOrder   *list.List
	orderIndex  map[string]*list.Element
}

func NewMemoryStore(maxBytes int64, evictPolicy EvictPolicy) *MemoryStore {
	return &MemoryStore{
		buckets:     make(map[string]map[string]Object),
		maxBytes:    maxBytes,
		evictPolicy: evictPolicy,
		fifoOrder:   list.New(),
		orderIndex:  make(map[string]*list.Element),
	}
}

func (s *MemoryStore) CreateBucket(name string) {
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
	b, ok := s.buckets[name]
	if !ok {
		return false
	}

	for key := range b {
		_ = s.removeObjectLocked(name, key)
	}
	delete(s.buckets, name)
	return true
}

func (s *MemoryStore) PutObject(bucket, key string, body []byte) (Object, error) {
	sum := md5.Sum(body)
	obj := Object{
		Body:         append([]byte(nil), body...),
		ETag:         `"` + hex.EncodeToString(sum[:]) + `"`,
		LastModified: time.Now().UTC(),
	}
	newSize := int64(len(obj.Body))
	currentID := makeObjectID(bucket, key)

	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[bucket]
	if !ok {
		b = make(map[string]Object)
		s.buckets[bucket] = b
	}

	oldObj, oldExists := b[key]
	oldSize := int64(0)
	if oldExists {
		oldSize = int64(len(oldObj.Body))
	}

	if err := s.ensureCapacityLocked(currentID, oldExists, oldSize, newSize); err != nil {
		return Object{}, err
	}

	if oldExists {
		_ = s.removeObjectLocked(bucket, key)
	}
	s.insertObjectLocked(bucket, key, obj)

	return obj, nil
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
	return s.removeObjectLocked(bucket, key)
}

func (s *MemoryStore) ListObjects(bucket, prefix string) ([]ListedObject, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buckets[bucket]
	if !ok {
		return nil, false
	}

	out := make([]ListedObject, 0, len(b))
	for k, v := range b {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		out = append(out, ListedObject{Key: k, Object: v})
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
	for name := range s.buckets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *MemoryStore) ensureCapacityLocked(currentID string, hasCurrent bool, currentSize, newSize int64) error {
	if s.maxBytes <= 0 {
		return nil
	}

	effectiveUsed := s.usedBytes
	if hasCurrent {
		effectiveUsed -= currentSize
	}
	targetUsed := effectiveUsed + newSize
	if targetUsed <= s.maxBytes {
		return nil
	}

	switch s.evictPolicy {
	case EvictPolicyReject:
		return fmt.Errorf("%w: need=%d used=%d limit=%d", ErrCapacityExceeded, newSize, effectiveUsed, s.maxBytes)
	case EvictPolicyFIFO:
		needFree := targetUsed - s.maxBytes
		var (
			freed    int64
			evictIDs []string
		)
		for e := s.fifoOrder.Front(); e != nil && freed < needFree; e = e.Next() {
			id, _ := e.Value.(string)
			if hasCurrent && id == currentID {
				continue
			}
			evictObj, ok := s.lookupObjectByIDLocked(id)
			if !ok {
				continue
			}
			evictIDs = append(evictIDs, id)
			freed += int64(len(evictObj.Body))
		}

		if freed < needFree {
			return fmt.Errorf("%w: need=%d used=%d limit=%d", ErrCapacityExceeded, newSize, effectiveUsed, s.maxBytes)
		}

		for _, id := range evictIDs {
			s.evictObjectByIDLocked(id)
		}
		return nil
	default:
		return fmt.Errorf("unsupported eviction policy %q", s.evictPolicy)
	}
}

func makeObjectID(bucket, key string) string {
	return bucket + "\x00" + key
}

func splitObjectID(id string) (bucket, key string, ok bool) {
	idx := strings.IndexByte(id, '\x00')
	if idx <= 0 {
		return "", "", false
	}
	return id[:idx], id[idx+1:], true
}

func (s *MemoryStore) lookupObjectByIDLocked(id string) (Object, bool) {
	bucket, key, ok := splitObjectID(id)
	if !ok {
		return Object{}, false
	}
	b, ok := s.buckets[bucket]
	if !ok {
		return Object{}, false
	}
	obj, ok := b[key]
	return obj, ok
}

func (s *MemoryStore) evictObjectByIDLocked(id string) {
	bucket, key, ok := splitObjectID(id)
	if !ok {
		return
	}
	_ = s.removeObjectLocked(bucket, key)
}

func (s *MemoryStore) removeObjectLocked(bucket, key string) bool {
	b, ok := s.buckets[bucket]
	if !ok {
		return false
	}
	obj, ok := b[key]
	if !ok {
		return false
	}

	delete(b, key)
	s.usedBytes -= int64(len(obj.Body))

	id := makeObjectID(bucket, key)
	if elem, ok := s.orderIndex[id]; ok {
		s.fifoOrder.Remove(elem)
		delete(s.orderIndex, id)
	}
	return true
}

func (s *MemoryStore) insertObjectLocked(bucket, key string, obj Object) {
	b, ok := s.buckets[bucket]
	if !ok {
		b = make(map[string]Object)
		s.buckets[bucket] = b
	}
	b[key] = obj
	s.usedBytes += int64(len(obj.Body))

	id := makeObjectID(bucket, key)
	if elem, ok := s.orderIndex[id]; ok {
		s.fifoOrder.Remove(elem)
	}
	s.orderIndex[id] = s.fifoOrder.PushBack(id)
}
