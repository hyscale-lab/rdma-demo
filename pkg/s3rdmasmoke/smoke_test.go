package s3rdmasmoke

import (
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
