package control

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"rdma-demo/server-client-demo/internal/inmems3/payload"
	"rdma-demo/server-client-demo/internal/inmems3/store"
	controlv1 "rdma-demo/server-client-demo/proto/inmems3/control/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestControlServiceAddFolderAndCleanup(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "bench-bucket", "input", "part-00000.csv"), "payload")

	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	loader := payload.NewLoader(memStore)

	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	RegisterGRPC(grpcServer, loader)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
	})

	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.DialContext: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client := controlv1.NewControlServiceClient(conn)

	addResp, err := client.AddFolder(ctx, &controlv1.AddFolderRequest{Path: root})
	if err != nil {
		t.Fatalf("AddFolder: %v", err)
	}
	if got, want := addResp.GetBucketsLoaded(), uint32(1); got != want {
		t.Fatalf("BucketsLoaded = %d, want %d", got, want)
	}

	listResp, err := client.ListBuckets(ctx, &controlv1.ListBucketsRequest{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if got, want := len(listResp.GetBuckets()), 1; got != want {
		t.Fatalf("bucket count = %d, want %d", got, want)
	}

	clearResp, err := client.ClearAll(ctx, &controlv1.ClearAllRequest{})
	if err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if got, want := clearResp.GetBucketsDeleted(), uint32(1); got != want {
		t.Fatalf("BucketsDeleted = %d, want %d", got, want)
	}
}

func mustWriteFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
