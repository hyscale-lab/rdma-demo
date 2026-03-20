package s3api

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rdma-demo/server-client-demo/internal/s3rdmaserver/store"
)

func TestHandlerBucketLifecycleAndRootList(t *testing.T) {
	memStore := store.NewMemoryStore()
	handler := NewHandler(memStore, "us-east-1", 1024)

	putBucketReq := httptest.NewRequest(http.MethodPut, "/bench-bucket", nil)
	putBucketRes := httptest.NewRecorder()
	handler.ServeHTTP(putBucketRes, putBucketReq)
	if putBucketRes.Code != http.StatusOK {
		t.Fatalf("bucket PUT status = %d, want %d", putBucketRes.Code, http.StatusOK)
	}

	headBucketReq := httptest.NewRequest(http.MethodHead, "/bench-bucket", nil)
	headBucketRes := httptest.NewRecorder()
	handler.ServeHTTP(headBucketRes, headBucketReq)
	if headBucketRes.Code != http.StatusOK {
		t.Fatalf("bucket HEAD status = %d, want %d", headBucketRes.Code, http.StatusOK)
	}

	rootListReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootListRes := httptest.NewRecorder()
	handler.ServeHTTP(rootListRes, rootListReq)
	if rootListRes.Code != http.StatusOK {
		t.Fatalf("root GET status = %d, want %d", rootListRes.Code, http.StatusOK)
	}
	if body := rootListRes.Body.String(); !strings.Contains(body, "<Name>bench-bucket</Name>") {
		t.Fatalf("root list body missing bucket name: %s", body)
	}

	deleteBucketReq := httptest.NewRequest(http.MethodDelete, "/bench-bucket", nil)
	deleteBucketRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteBucketRes, deleteBucketReq)
	if deleteBucketRes.Code != http.StatusNoContent {
		t.Fatalf("bucket DELETE status = %d, want %d", deleteBucketRes.Code, http.StatusNoContent)
	}
}

func TestHandlerObjectGetHeadDeleteAndBucketList(t *testing.T) {
	memStore := store.NewMemoryStore()
	modTime := time.Date(2026, 3, 20, 9, 0, 0, 0, time.UTC)
	memStore.PutLoadedObject("bench-bucket", "input/part-00000.csv", []byte("seed-payload"), modTime)
	memStore.PutLoadedObject("bench-bucket", "other-key", []byte("other"), modTime)
	handler := NewHandler(memStore, "us-east-1", 1024)

	getReq := httptest.NewRequest(http.MethodGet, "/bench-bucket/input/part-00000.csv", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("object GET status = %d, want %d", getRes.Code, http.StatusOK)
	}
	body, err := io.ReadAll(getRes.Result().Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	if got, want := string(body), "seed-payload"; got != want {
		t.Fatalf("GET body = %q, want %q", got, want)
	}

	headReq := httptest.NewRequest(http.MethodHead, "/bench-bucket/input/part-00000.csv", nil)
	headRes := httptest.NewRecorder()
	handler.ServeHTTP(headRes, headReq)
	if headRes.Code != http.StatusOK {
		t.Fatalf("object HEAD status = %d, want %d", headRes.Code, http.StatusOK)
	}
	if got, want := headRes.Header().Get("Content-Length"), "12"; got != want {
		t.Fatalf("HEAD Content-Length = %q, want %q", got, want)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/bench-bucket?prefix=input/&max-keys=1", nil)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("bucket GET status = %d, want %d", listRes.Code, http.StatusOK)
	}
	listBody := listRes.Body.String()
	if !strings.Contains(listBody, "<Key>input/part-00000.csv</Key>") {
		t.Fatalf("bucket list missing filtered key: %s", listBody)
	}
	if strings.Contains(listBody, "<Key>other-key</Key>") {
		t.Fatalf("bucket list unexpectedly included non-prefix key: %s", listBody)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/bench-bucket/input/part-00000.csv", nil)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("object DELETE status = %d, want %d", deleteRes.Code, http.StatusNoContent)
	}

	getAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/bench-bucket/input/part-00000.csv", nil)
	getAfterDeleteRes := httptest.NewRecorder()
	handler.ServeHTTP(getAfterDeleteRes, getAfterDeleteReq)
	if getAfterDeleteRes.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, want %d", getAfterDeleteRes.Code, http.StatusNotFound)
	}
}

func TestHandlerPutReturnsETagAndDoesNotCreateReadableObject(t *testing.T) {
	memStore := store.NewMemoryStore()
	memStore.CreateBucket("bench-bucket")
	handler := NewHandler(memStore, "us-east-1", 1024)

	putReq := httptest.NewRequest(http.MethodPut, "/bench-bucket/seed-object", strings.NewReader("hello world"))
	putRes := httptest.NewRecorder()
	handler.ServeHTTP(putRes, putReq)

	if putRes.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", putRes.Code, http.StatusOK)
	}
	sum := md5.Sum([]byte("hello world"))
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`
	if got := putRes.Header().Get("ETag"); got != wantETag {
		t.Fatalf("PUT ETag = %q, want %q", got, wantETag)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/bench-bucket/seed-object", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d", getRes.Code, http.StatusNotFound)
	}
}

func TestHandlerPutRejectsTooLargeObject(t *testing.T) {
	memStore := store.NewMemoryStore()
	handler := NewHandler(memStore, "us-east-1", 4)

	req := httptest.NewRequest(http.MethodPut, "/bench-bucket/too-large", strings.NewReader("hello"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want %d", res.Code, http.StatusBadRequest)
	}

	if _, ok := memStore.GetObject("bench-bucket", "too-large"); ok {
		t.Fatal("unexpected readable object after oversized PUT")
	}
}

func TestHandlerPutDoesNotOverwritePreloadedObject(t *testing.T) {
	memStore := store.NewMemoryStore()
	memStore.PutLoadedObject("bench-bucket", "seed-object", []byte("payload-from-loader"), time.Now().UTC())
	handler := NewHandler(memStore, "us-east-1", 1024)

	putReq := httptest.NewRequest(http.MethodPut, "/bench-bucket/seed-object", strings.NewReader("uploaded-body"))
	putRes := httptest.NewRecorder()
	handler.ServeHTTP(putRes, putReq)
	if putRes.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", putRes.Code, http.StatusOK)
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
