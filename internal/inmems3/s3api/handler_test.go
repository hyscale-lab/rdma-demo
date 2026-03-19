package s3api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rdma-demo/server-client-demo/internal/inmems3/ingest"
	"rdma-demo/server-client-demo/internal/inmems3/store"
)

func TestHandlerPutReturnsETagAndDoesNotCreateReadableObject(t *testing.T) {
	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	memStore.CreateBucket("bench-bucket")
	handler := NewHandler(memStore, "us-east-1", 1024)

	putReq := httptest.NewRequest(http.MethodPut, "/bench-bucket/seed-object", strings.NewReader("hello world"))
	putRes := httptest.NewRecorder()
	handler.ServeHTTP(putRes, putReq)

	if putRes.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", putRes.Code, http.StatusOK)
	}
	if got, want := putRes.Header().Get("ETag"), ingest.ResultForBytes([]byte("hello world")).ETag; got != want {
		t.Fatalf("PUT ETag = %q, want %q", got, want)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/bench-bucket/seed-object", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)

	if getRes.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d", getRes.Code, http.StatusNotFound)
	}
}

func TestHandlerPutRejectsTooLargeObject(t *testing.T) {
	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	handler := NewHandler(memStore, "us-east-1", 4)

	req := httptest.NewRequest(http.MethodPut, "/bench-bucket/too-large", strings.NewReader("hello"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want %d", res.Code, http.StatusBadRequest)
	}

	obj, ok := memStore.GetObject("bench-bucket", "too-large")
	if ok {
		t.Fatalf("unexpected stored object after oversized PUT: %+v", obj)
	}
}

func TestHandlerPutDoesNotOverwritePreloadedObject(t *testing.T) {
	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	if _, err := memStore.PutObject("bench-bucket", "seed-object", []byte("payload-from-loader")); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	handler := NewHandler(memStore, "us-east-1", 1024)

	putReq := httptest.NewRequest(http.MethodPut, "/bench-bucket/seed-object", strings.NewReader("uploaded-body"))
	putRes := httptest.NewRecorder()
	handler.ServeHTTP(putRes, putReq)

	if putRes.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", putRes.Code, http.StatusOK)
	}
	if got, want := putRes.Header().Get("ETag"), ingest.ResultForBytes([]byte("uploaded-body")).ETag; got != want {
		t.Fatalf("PUT ETag = %q, want %q", got, want)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/bench-bucket/seed-object", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)

	if getRes.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getRes.Code, http.StatusOK)
	}
	body, err := io.ReadAll(getRes.Result().Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	if got, want := string(body), "payload-from-loader"; got != want {
		t.Fatalf("GET body = %q, want %q", got, want)
	}
}

func TestSplitBucketAndKey(t *testing.T) {
	bucket, key, err := SplitBucketAndKey("/bench-bucket/input_payload/mapper/part-00000.csv")
	if err != nil {
		t.Fatalf("SplitBucketAndKey returned error: %v", err)
	}
	if bucket != "bench-bucket" {
		t.Fatalf("bucket = %q, want %q", bucket, "bench-bucket")
	}
	if key != "input_payload/mapper/part-00000.csv" {
		t.Fatalf("key = %q, want %q", key, "input_payload/mapper/part-00000.csv")
	}
}
