package s3rdmasmoke

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigNormalizedDefaultsEndpointByTransport(t *testing.T) {
	tcpCfg := Config{
		Transport:      "tcp",
		Bucket:         "bench-bucket",
		Key:            "object",
		Op:             "get",
		PayloadSize:    128,
		RequestTimeout: time.Second,
	}
	if got, want := tcpCfg.normalized().Endpoint, defaultTCPEndpoint; got != want {
		t.Fatalf("tcp endpoint = %q, want %q", got, want)
	}

	rdmaCfg := Config{
		Transport:      "rdma",
		Bucket:         "bench-bucket",
		Key:            "object",
		Op:             "get",
		PayloadSize:    128,
		RequestTimeout: time.Second,
		RDMASharedMem:  16 << 20,
		RDMAGetMaxSize: 1024,
	}
	normalized := rdmaCfg.normalized()
	if got, want := normalized.Endpoint, defaultRDMAEndpoint; got != want {
		t.Fatalf("rdma endpoint = %q, want %q", got, want)
	}
	if got, want := normalized.RDMAConnectTO, defaultRDMAConnectTO; got != want {
		t.Fatalf("rdma connect timeout = %s, want %s", got, want)
	}
	if got, want := normalized.RDMAControlTO, defaultRDMAControlTO; got != want {
		t.Fatalf("rdma control timeout = %s, want %s", got, want)
	}
	if got, want := normalized.RDMADataTO, defaultRDMADataTO; got != want {
		t.Fatalf("rdma data timeout = %s, want %s", got, want)
	}
}

func TestConfigValidateRejectsInvalidRDMARanges(t *testing.T) {
	cfg := Config{
		Transport:      "rdma",
		Endpoint:       defaultRDMAEndpoint,
		Bucket:         "bench-bucket",
		Key:            "object",
		Op:             "put",
		PayloadSize:    1024,
		RequestTimeout: time.Second,
		RDMASharedMem:  512,
		RDMAPutOffset:  0,
		RDMAGetOffset:  0,
		RDMAGetMaxSize: 256,
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validate error for oversized rdma put range")
	}
}

func TestNewSharedMemoryWithFileBackedPath(t *testing.T) {
	sharedMemPath := filepath.Join(t.TempDir(), "smoke-verify.img")

	shared, cleanup, err := newSharedMemory(4096, sharedMemPath)
	if err != nil {
		t.Fatalf("newSharedMemory() error = %v", err)
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Fatalf("cleanup() error = %v", cleanupErr)
		}
	}()

	if len(shared) != 4096 {
		t.Fatalf("len(shared) = %d, want %d", len(shared), 4096)
	}

	info, err := os.Stat(sharedMemPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", sharedMemPath, err)
	}
	if info.Size() != 4096 {
		t.Fatalf("file size = %d, want %d", info.Size(), 4096)
	}
}

func TestNewSharedMemoryWithAnonymousFallback(t *testing.T) {
	shared, cleanup, err := newSharedMemory(2048, "")
	if err != nil {
		t.Fatalf("newSharedMemory() error = %v", err)
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Fatalf("cleanup() error = %v", cleanupErr)
		}
	}()

	if len(shared) != 2048 {
		t.Fatalf("len(shared) = %d, want %d", len(shared), 2048)
	}
}

func TestSummaryIncludesOperationSpecificFields(t *testing.T) {
	summary := Result{
		Transport:   transportRDMA,
		Endpoint:    defaultRDMAEndpoint,
		Bucket:      "bench-bucket",
		Key:         "object",
		Operation:   opPutGet,
		PutAccepted: true,
		GetMissing:  true,
		Verified:    true,
	}.Summary()

	if want := "put_accepted=true"; !contains(summary, want) {
		t.Fatalf("summary %q missing %q", summary, want)
	}
	if want := "get_missing=true"; !contains(summary, want) {
		t.Fatalf("summary %q missing %q", summary, want)
	}
}

func contains(in, want string) bool {
	return strings.Contains(in, want)
}
