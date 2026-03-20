package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Loader performs the startup-only payload-root import into the GET store.
type Loader struct {
	store *MemoryStore
}

type LoadResult struct {
	Buckets       []string
	BucketsLoaded int
	ObjectsLoaded int
	BytesLoaded   int64
}

type pendingObject struct {
	key          string
	body         []byte
	lastModified time.Time
}

func NewLoader(store *MemoryStore) *Loader {
	return &Loader{store: store}
}

func (l *Loader) LoadRoot(root string) (LoadResult, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return LoadResult{}, fmt.Errorf("payload root must not be empty")
	}

	info, err := os.Stat(root)
	if err != nil {
		return LoadResult{}, err
	}
	if !info.IsDir() {
		return LoadResult{}, fmt.Errorf("payload root %q is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return LoadResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	result := LoadResult{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		bucket := strings.TrimSpace(entry.Name())
		if bucket == "" {
			continue
		}

		objects, err := loadBucketObjects(filepath.Join(root, entry.Name()))
		if err != nil {
			return result, fmt.Errorf("load bucket %q: %w", bucket, err)
		}

		l.store.CreateBucket(bucket)
		for _, obj := range objects {
			l.store.PutLoadedObject(bucket, obj.key, obj.body, obj.lastModified)
			result.ObjectsLoaded++
			result.BytesLoaded += int64(len(obj.body))
		}
		result.Buckets = append(result.Buckets, bucket)
		result.BucketsLoaded++
	}

	return result, nil
}

func loadBucketObjects(bucketDir string) ([]pendingObject, error) {
	var objects []pendingObject
	err := filepath.WalkDir(bucketDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bucketDir, path)
		if err != nil {
			return err
		}

		objects = append(objects, pendingObject{
			key:          filepath.ToSlash(rel),
			body:         body,
			lastModified: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].key < objects[j].key
	})
	return objects, nil
}
