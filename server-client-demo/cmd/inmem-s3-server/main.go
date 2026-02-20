package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	awsrdmahttp "github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma"
)

const s3XMLNS = "http://s3.amazonaws.com/doc/2006-03-01/"

type serverConfig struct {
	tcpListen     string
	enableRDMA    bool
	rdmaListen    string
	rdmaBacklog   int
	rdmaWorkers   int
	region        string
	maxObjectSize int64
}

type runningServer struct {
	name string
	ln   net.Listener
	srv  *http.Server
}

func main() {
	cfg := serverConfig{}
	flag.StringVar(&cfg.tcpListen, "tcp-listen", "127.0.0.1:9000", "TCP listen address, empty disables TCP")
	flag.BoolVar(&cfg.enableRDMA, "enable-rdma", false, "enable RDMA verbs listener")
	flag.StringVar(&cfg.rdmaListen, "rdma-listen", "127.0.0.1:19090", "RDMA listen address")
	flag.IntVar(&cfg.rdmaBacklog, "rdma-backlog", awsrdmahttp.DefaultVerbsListenBacklog, "RDMA listen backlog")
	flag.IntVar(&cfg.rdmaWorkers, "rdma-accept-workers", awsrdmahttp.DefaultVerbsAcceptWorkers, "RDMA accept worker count")
	flag.StringVar(&cfg.region, "region", "us-east-1", "region returned by server")
	flag.Int64Var(&cfg.maxObjectSize, "max-object-size", 64<<20, "max object size in bytes, <=0 means unlimited")
	flag.Parse()

	if cfg.tcpListen == "" && !cfg.enableRDMA {
		log.Fatal("at least one listener must be enabled")
	}

	store := newMemoryStore()
	handler := &s3Handler{
		store:         store,
		region:        cfg.region,
		maxObjectSize: cfg.maxObjectSize,
	}

	var servers []*runningServer

	if cfg.tcpListen != "" {
		ln, err := net.Listen("tcp", cfg.tcpListen)
		if err != nil {
			log.Fatalf("listen tcp %s: %v", cfg.tcpListen, err)
		}
		servers = append(servers, &runningServer{
			name: "tcp",
			ln:   ln,
			srv: &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			},
		})
	}

	if cfg.enableRDMA {
		ln, err := awsrdmahttp.NewVerbsListener("rdma", cfg.rdmaListen, awsrdmahttp.VerbsListenerOptions{
			VerbsOptions:  awsrdmahttp.VerbsOptions{},
			Backlog:       cfg.rdmaBacklog,
			AcceptWorkers: cfg.rdmaWorkers,
		})
		if err != nil {
			log.Fatalf("listen rdma %s: %v", cfg.rdmaListen, err)
		}
		servers = append(servers, &runningServer{
			name: "rdma",
			ln:   ln,
			srv: &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			},
		})
	}

	if len(servers) == 0 {
		log.Fatal("no listener started")
	}

	errCh := make(chan error, len(servers))
	for _, rs := range servers {
		rs := rs
		log.Printf("%s listening on %s", rs.name, rs.ln.Addr())
		go func() {
			if err := rs.srv.Serve(rs.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s serve: %w", rs.name, err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var shutdownReason string
	select {
	case sig := <-sigCh:
		shutdownReason = fmt.Sprintf("signal=%s", sig.String())
	case err := <-errCh:
		shutdownReason = fmt.Sprintf("serve_error=%v", err)
	}
	log.Printf("shutting down: %s", shutdownReason)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, rs := range servers {
		if err := rs.srv.Shutdown(ctx); err != nil {
			log.Printf("%s shutdown: %v", rs.name, err)
		}
	}
}

type memoryStore struct {
	mu      sync.RWMutex
	buckets map[string]map[string]storedObject
}

type storedObject struct {
	body         []byte
	etag         string
	lastModified time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		buckets: make(map[string]map[string]storedObject),
	}
}

func (s *memoryStore) createBucket(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[name]; !ok {
		s.buckets[name] = make(map[string]storedObject)
	}
}

func (s *memoryStore) bucketExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.buckets[name]
	return ok
}

func (s *memoryStore) deleteBucket(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[name]; !ok {
		return false
	}
	delete(s.buckets, name)
	return true
}

func (s *memoryStore) putObject(bucket, key string, body []byte) storedObject {
	sum := md5.Sum(body)
	obj := storedObject{
		body:         append([]byte(nil), body...),
		etag:         `"` + hex.EncodeToString(sum[:]) + `"`,
		lastModified: time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[bucket]
	if !ok {
		b = make(map[string]storedObject)
		s.buckets[bucket] = b
	}
	b[key] = obj
	return obj
}

func (s *memoryStore) getObject(bucket, key string) (storedObject, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.buckets[bucket]
	if !ok {
		return storedObject{}, false
	}
	obj, ok := b[key]
	return obj, ok
}

func (s *memoryStore) deleteObject(bucket, key string) bool {
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

type listedObject struct {
	key string
	obj storedObject
}

func (s *memoryStore) listObjects(bucket, prefix string) ([]listedObject, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buckets[bucket]
	if !ok {
		return nil, false
	}

	out := make([]listedObject, 0, len(b))
	for k, v := range b {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		out = append(out, listedObject{key: k, obj: v})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].key < out[j].key
	})
	return out, true
}

func (s *memoryStore) listBuckets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.buckets))
	for name := range s.buckets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type s3Handler struct {
	store         *memoryStore
	region        string
	maxObjectSize int64
	requestID     atomic.Uint64
}

func (h *s3Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := strconv.FormatUint(h.requestID.Add(1), 10)
	w.Header().Set("x-amz-request-id", reqID)
	w.Header().Set("x-amz-id-2", "rdma-demo")
	w.Header().Set("x-amz-bucket-region", h.region)

	if r.URL.Path == "/" {
		if r.Method == http.MethodGet {
			h.writeListBuckets(w)
			return
		}
		writeS3Error(w, http.StatusMethodNotAllowed, reqID, "MethodNotAllowed", "unsupported method", "", "")
		return
	}

	bucket, key, err := splitBucketAndKey(r.URL.Path)
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, reqID, "InvalidURI", err.Error(), "", "")
		return
	}

	if key == "" {
		h.handleBucket(w, r, reqID, bucket)
		return
	}
	h.handleObject(w, r, reqID, bucket, key)
}

func (h *s3Handler) handleBucket(w http.ResponseWriter, r *http.Request, reqID, bucket string) {
	switch r.Method {
	case http.MethodPut:
		h.store.createBucket(bucket)
		writeXML(w, http.StatusOK, createBucketResult{
			XMLNS:    s3XMLNS,
			Location: "/" + bucket,
		})
	case http.MethodHead:
		if !h.store.bucketExists(bucket) {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchBucket", "bucket not found", bucket, "")
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		prefix := r.URL.Query().Get("prefix")
		objects, ok := h.store.listObjects(bucket, prefix)
		if !ok {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchBucket", "bucket not found", bucket, "")
			return
		}

		maxKeys := 1000
		if q := r.URL.Query().Get("max-keys"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 {
				maxKeys = v
			}
		}
		truncated := false
		if len(objects) > maxKeys {
			objects = objects[:maxKeys]
			truncated = true
		}

		contents := make([]listContent, 0, len(objects))
		for _, obj := range objects {
			contents = append(contents, listContent{
				Key:          obj.key,
				LastModified: obj.obj.lastModified.Format(time.RFC3339),
				ETag:         obj.obj.etag,
				Size:         int64(len(obj.obj.body)),
				StorageClass: "STANDARD",
			})
		}

		writeXML(w, http.StatusOK, listBucketResult{
			XMLNS:       s3XMLNS,
			Name:        bucket,
			Prefix:      prefix,
			KeyCount:    len(contents),
			MaxKeys:     maxKeys,
			IsTruncated: truncated,
			Contents:    contents,
		})
	case http.MethodDelete:
		if !h.store.deleteBucket(bucket) {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchBucket", "bucket not found", bucket, "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, reqID, "MethodNotAllowed", "unsupported method", bucket, "")
	}
}

func (h *s3Handler) handleObject(w http.ResponseWriter, r *http.Request, reqID, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		body, err := h.readBody(r.Body)
		if err != nil {
			writeS3Error(w, http.StatusBadRequest, reqID, "InvalidRequest", err.Error(), bucket, key)
			return
		}
		obj := h.store.putObject(bucket, key, body)
		w.Header().Set("ETag", obj.etag)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		obj, ok := h.store.getObject(bucket, key)
		if !ok {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchKey", "key not found", bucket, key)
			return
		}
		w.Header().Set("ETag", obj.etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		w.Header().Set("Last-Modified", obj.lastModified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.body)
	case http.MethodHead:
		obj, ok := h.store.getObject(bucket, key)
		if !ok {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchKey", "key not found", bucket, key)
			return
		}
		w.Header().Set("ETag", obj.etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		w.Header().Set("Last-Modified", obj.lastModified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		_ = h.store.deleteObject(bucket, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, reqID, "MethodNotAllowed", "unsupported method", bucket, key)
	}
}

func (h *s3Handler) readBody(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	if h.maxObjectSize <= 0 {
		return io.ReadAll(body)
	}

	limited := io.LimitReader(body, h.maxObjectSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > h.maxObjectSize {
		return nil, fmt.Errorf("object too large: %d > %d", len(data), h.maxObjectSize)
	}
	return data, nil
}

func (h *s3Handler) writeListBuckets(w http.ResponseWriter) {
	buckets := h.store.listBuckets()
	items := make([]bucketInfo, 0, len(buckets))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, name := range buckets {
		items = append(items, bucketInfo{
			Name:         name,
			CreationDate: now,
		})
	}

	writeXML(w, http.StatusOK, listAllMyBucketsResult{
		XMLNS: s3XMLNS,
		Owner: ownerInfo{
			ID:          "rdma-demo",
			DisplayName: "rdma-demo",
		},
		Buckets: bucketsList{
			Bucket: items,
		},
	})
}

func splitBucketAndKey(path string) (bucket, key string, err error) {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", "", fmt.Errorf("missing bucket name")
	}
	parts := strings.SplitN(trimmed, "/", 2)
	bucket, err = url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("invalid bucket path: %w", err)
	}
	if bucket == "" {
		return "", "", fmt.Errorf("missing bucket name")
	}

	if len(parts) == 1 {
		return bucket, "", nil
	}
	key, err = url.PathUnescape(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("invalid key path: %w", err)
	}
	return bucket, key, nil
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(v)
}

func writeS3Error(w http.ResponseWriter, status int, reqID, code, message, bucket, key string) {
	writeXML(w, status, s3ErrorResponse{
		Code:      code,
		Message:   message,
		Bucket:    bucket,
		Key:       key,
		RequestID: reqID,
	})
}

type s3ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Bucket    string   `xml:"BucketName,omitempty"`
	Key       string   `xml:"Key,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

type createBucketResult struct {
	XMLName  xml.Name `xml:"CreateBucketResult"`
	XMLNS    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
}

type listContent struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type listBucketResult struct {
	XMLName     xml.Name      `xml:"ListBucketResult"`
	XMLNS       string        `xml:"xmlns,attr"`
	Name        string        `xml:"Name"`
	Prefix      string        `xml:"Prefix"`
	KeyCount    int           `xml:"KeyCount"`
	MaxKeys     int           `xml:"MaxKeys"`
	IsTruncated bool          `xml:"IsTruncated"`
	Contents    []listContent `xml:"Contents"`
}

type listAllMyBucketsResult struct {
	XMLName xml.Name    `xml:"ListAllMyBucketsResult"`
	XMLNS   string      `xml:"xmlns,attr"`
	Owner   ownerInfo   `xml:"Owner"`
	Buckets bucketsList `xml:"Buckets"`
}

type ownerInfo struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type bucketsList struct {
	Bucket []bucketInfo `xml:"Bucket"`
}

type bucketInfo struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}
