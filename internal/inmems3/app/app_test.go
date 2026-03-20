package app

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestUsageTextDocumentsKeyDefaults(t *testing.T) {
	text := DefaultConfig().UsageText("inmem-s3-server")

	for _, want := range []string{
		"Usage:\n  inmem-s3-server [flags]",
		"Data listeners (enable at least one):",
		"--tcp-listen addr",
		defaultTCPListen,
		"--enable-rdma-zcopy",
		"--rdma-zcopy-listen addr",
		defaultRDMAZCopyListen,
		"--payload-root path",
		"--control-grpc-listen addr",
		"--store-evict-policy string",
		"--rdma-send-signal-interval int",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage text missing %q\n%s", want, text)
		}
	}
}

func TestNewFlagSetHelpUsesCustomUsage(t *testing.T) {
	cfg := DefaultConfig()
	var buf bytes.Buffer

	fs := NewFlagSet("inmem-s3-server", &buf, &cfg)
	err := fs.Parse([]string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}

	help := buf.String()
	for _, want := range []string{
		"S3-compatible in-memory benchmark server.",
		"General behavior:",
		"In-memory payload store:",
		"RDMA tuning:",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q\n%s", want, help)
		}
	}
}

func TestNormalizedAddsDefaultRDMAListenAddress(t *testing.T) {
	cfg := Config{
		TCPListen:        defaultTCPListen,
		EnableRDMAZCopy:  true,
		Region:           defaultRegion,
		MaxObjectSize:    defaultMaxObjectSize,
		StoreEvictPolicy: "reject",
	}

	got := cfg.normalized()
	if got.RDMAZCopyListen != defaultRDMAZCopyListen {
		t.Fatalf("expected rdma listen %q, got %q", defaultRDMAZCopyListen, got.RDMAZCopyListen)
	}
}

func TestValidateRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "no data listener",
			cfg: Config{
				Region:           defaultRegion,
				MaxObjectSize:    defaultMaxObjectSize,
				StoreEvictPolicy: "reject",
			},
			wantErr: "at least one data listener",
		},
		{
			name: "empty region",
			cfg: func() Config {
				cfg := DefaultConfig()
				cfg.Region = ""
				return cfg
			}(),
			wantErr: "region must not be empty",
		},
		{
			name: "empty rdma listen when enabled",
			cfg: func() Config {
				cfg := DefaultConfig()
				cfg.EnableRDMAZCopy = true
				cfg.RDMAZCopyListen = ""
				return cfg
			}(),
			wantErr: "rdma-zcopy-listen must not be empty",
		},
		{
			name: "negative rdma backlog",
			cfg: func() Config {
				cfg := DefaultConfig()
				cfg.RDMABacklog = -1
				return cfg
			}(),
			wantErr: "rdma-backlog must be >= 0",
		},
		{
			name: "negative rdma workers",
			cfg: func() Config {
				cfg := DefaultConfig()
				cfg.RDMAWorkers = -1
				return cfg
			}(),
			wantErr: "rdma-accept-workers must be >= 0",
		},
		{
			name: "negative store bytes",
			cfg: func() Config {
				cfg := DefaultConfig()
				cfg.StoreMaxBytes = -1
				return cfg
			}(),
			wantErr: "store-max-bytes must be >= 0",
		},
		{
			name: "invalid evict policy",
			cfg: func() Config {
				cfg := DefaultConfig()
				cfg.StoreEvictPolicy = "nope"
				return cfg
			}(),
			wantErr: "unsupported store eviction policy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
