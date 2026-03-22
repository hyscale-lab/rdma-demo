package s3rdmaclient

import (
	"testing"
	"time"
)

func TestResolveTimeoutsUsesSplitDefaults(t *testing.T) {
	got := resolveTimeouts(Config{})

	if got.connect != defaultConnectTimeout {
		t.Fatalf("connect timeout = %s, want %s", got.connect, defaultConnectTimeout)
	}
	if got.control != defaultControlTimeout {
		t.Fatalf("control timeout = %s, want %s", got.control, defaultControlTimeout)
	}
	if got.data != defaultDataTimeout {
		t.Fatalf("data timeout = %s, want %s", got.data, defaultDataTimeout)
	}
}

func TestResolveTimeoutsUsesLegacyFallbackForUnsetValues(t *testing.T) {
	got := resolveTimeouts(Config{
		RequestTimeout: 45 * time.Second,
		ControlTimeout: 12 * time.Second,
	})

	if got.connect != 45*time.Second {
		t.Fatalf("connect timeout = %s, want %s", got.connect, 45*time.Second)
	}
	if got.control != 12*time.Second {
		t.Fatalf("control timeout = %s, want %s", got.control, 12*time.Second)
	}
	if got.data != 45*time.Second {
		t.Fatalf("data timeout = %s, want %s", got.data, 45*time.Second)
	}
}
