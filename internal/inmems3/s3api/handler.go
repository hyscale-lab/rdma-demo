package s3api

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"rdma-demo/server-client-demo/internal/inmems3/ingest"
	"rdma-demo/server-client-demo/internal/inmems3/store"
)

const xmlNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

type Store interface {
	CreateBucket(name string)
	BucketExists(name string) bool
	DeleteBucket(name string) bool
	GetObject(bucket, key string) (store.Object, bool)
	DeleteObject(bucket, key string) bool
	ListObjects(bucket, prefix string) ([]store.ListedObject, bool)
	ListBuckets() []string
}

type Handler struct {
	store         Store
	region        string
	maxObjectSize int64
	requestID     atomic.Uint64
}

func NewHandler(store Store, region string, maxObjectSize int64) *Handler {
	return &Handler{
		store:         store,
		region:        region,
		maxObjectSize: maxObjectSize,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	bucket, key, err := SplitBucketAndKey(r.URL.Path)
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

func (h *Handler) handleBucket(w http.ResponseWriter, r *http.Request, reqID, bucket string) {
	switch r.Method {
	case http.MethodPut:
		h.store.CreateBucket(bucket)
		writeXML(w, http.StatusOK, createBucketResult{
			XMLNS:    xmlNamespace,
			Location: "/" + bucket,
		})
	case http.MethodHead:
		if !h.store.BucketExists(bucket) {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchBucket", "bucket not found", bucket, "")
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		prefix := r.URL.Query().Get("prefix")
		objects, ok := h.store.ListObjects(bucket, prefix)
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
				Key:          obj.Key,
				LastModified: obj.Object.LastModified.Format(time.RFC3339),
				ETag:         obj.Object.ETag,
				Size:         int64(len(obj.Object.Body)),
				StorageClass: "STANDARD",
			})
		}

		writeXML(w, http.StatusOK, listBucketResult{
			XMLNS:       xmlNamespace,
			Name:        bucket,
			Prefix:      prefix,
			KeyCount:    len(contents),
			MaxKeys:     maxKeys,
			IsTruncated: truncated,
			Contents:    contents,
		})
	case http.MethodDelete:
		if !h.store.DeleteBucket(bucket) {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchBucket", "bucket not found", bucket, "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, reqID, "MethodNotAllowed", "unsupported method", bucket, "")
	}
}

func (h *Handler) handleObject(w http.ResponseWriter, r *http.Request, reqID, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		result, err := ingest.ConsumeReader(r.Body, h.maxObjectSize)
		if err != nil {
			writeS3Error(w, http.StatusBadRequest, reqID, "InvalidRequest", err.Error(), bucket, key)
			return
		}
		h.store.CreateBucket(bucket)
		w.Header().Set("ETag", result.ETag)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		obj, ok := h.store.GetObject(bucket, key)
		if !ok {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchKey", "key not found", bucket, key)
			return
		}
		w.Header().Set("ETag", obj.ETag)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.Body)))
		w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.Body)
	case http.MethodHead:
		obj, ok := h.store.GetObject(bucket, key)
		if !ok {
			writeS3Error(w, http.StatusNotFound, reqID, "NoSuchKey", "key not found", bucket, key)
			return
		}
		w.Header().Set("ETag", obj.ETag)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.Body)))
		w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		_ = h.store.DeleteObject(bucket, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, reqID, "MethodNotAllowed", "unsupported method", bucket, key)
	}
}

func (h *Handler) writeListBuckets(w http.ResponseWriter) {
	buckets := h.store.ListBuckets()
	items := make([]bucketInfo, 0, len(buckets))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, name := range buckets {
		items = append(items, bucketInfo{
			Name:         name,
			CreationDate: now,
		})
	}

	writeXML(w, http.StatusOK, listAllMyBucketsResult{
		XMLNS: xmlNamespace,
		Owner: ownerInfo{
			ID:          "rdma-demo",
			DisplayName: "rdma-demo",
		},
		Buckets: bucketsList{
			Bucket: items,
		},
	})
}

func SplitBucketAndKey(path string) (bucket, key string, err error) {
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
