package payload

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hyscale-lab/rdma-demo/internal/inmems3/store"
)

type Store interface {
	CreateBucket(name string)
	DeleteBucket(name string) bool
	PutObject(bucket, key string, body []byte) (store.Object, error)
	ListBuckets() []string
}

type Loader struct {
	store Store
}

type AddFolderResult struct {
	Buckets       []string
	BucketsLoaded int
	ObjectsLoaded int
	BytesLoaded   int64
}

type pendingObject struct {
	key  string
	body []byte
}

func NewLoader(store Store) *Loader {
	return &Loader{store: store}
}

func (l *Loader) AddFolder(ctx context.Context, root string) (AddFolderResult, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return AddFolderResult{}, fmt.Errorf("payload root must not be empty")
	}

	info, err := os.Stat(root)
	if err != nil {
		return AddFolderResult{}, err
	}
	if !info.IsDir() {
		return AddFolderResult{}, fmt.Errorf("payload root %q is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return AddFolderResult{}, err
	}

	result := AddFolderResult{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !entry.IsDir() {
			continue
		}

		bucket := strings.TrimSpace(entry.Name())
		if bucket == "" {
			continue
		}

		bucketDir := filepath.Join(root, entry.Name())
		objects, err := loadBucketObjects(ctx, bucketDir)
		if err != nil {
			return result, fmt.Errorf("load bucket %q: %w", bucket, err)
		}

		l.store.DeleteBucket(bucket)
		l.store.CreateBucket(bucket)
		for _, obj := range objects {
			if _, err := l.store.PutObject(bucket, obj.key, obj.body); err != nil {
				return result, fmt.Errorf("store bucket %q key %q: %w", bucket, obj.key, err)
			}
			result.ObjectsLoaded++
			result.BytesLoaded += int64(len(obj.body))
		}

		result.Buckets = append(result.Buckets, bucket)
		result.BucketsLoaded++
	}

	sort.Strings(result.Buckets)
	return result, nil
}

func (l *Loader) DeleteBucket(name string) bool {
	return l.store.DeleteBucket(strings.TrimSpace(name))
}

func (l *Loader) ClearAll() int {
	buckets := l.store.ListBuckets()
	deleted := 0
	for _, bucket := range buckets {
		if l.store.DeleteBucket(bucket) {
			deleted++
		}
	}
	return deleted
}

func (l *Loader) ListBuckets() []string {
	return l.store.ListBuckets()
}

func loadBucketObjects(ctx context.Context, bucketDir string) ([]pendingObject, error) {
	var objects []pendingObject
	err := filepath.WalkDir(bucketDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		mode := d.Type()
		if !mode.IsRegular() {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bucketDir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		objects = append(objects, pendingObject{
			key:  key,
			body: body,
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
