package zcopy

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	awsrdmahttp "github.com/hyscale-lab/rdma-demo/pkg/rdma"
	"github.com/hyscale-lab/rdma-demo/pkg/rdma/zcopyproto"

	"github.com/hyscale-lab/rdma-demo/internal/inmems3/store"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "test" }
func (a fakeAddr) String() string  { return string(a) }

type sentAtRecord struct {
	payload []byte
	offset  int
}

type fakeZCopyConn struct {
	borrowed       *awsrdmahttp.BorrowedMessage
	borrowErr      error
	sentControl    [][]byte
	sentAt         []sentAtRecord
	closeCount     atomic.Int32
	recvMsgPayload [][]byte
}

func (c *fakeZCopyConn) SendMessage(_ context.Context, payload []byte) error {
	c.sentControl = append(c.sentControl, append([]byte(nil), payload...))
	return nil
}

func (c *fakeZCopyConn) RecvMessage(_ context.Context) ([]byte, error) {
	if len(c.recvMsgPayload) == 0 {
		return nil, net.ErrClosed
	}
	payload := append([]byte(nil), c.recvMsgPayload[0]...)
	c.recvMsgPayload = c.recvMsgPayload[1:]
	return payload, nil
}

func (c *fakeZCopyConn) Close() error {
	c.closeCount.Add(1)
	return nil
}

func (c *fakeZCopyConn) LocalAddr() net.Addr  { return fakeAddr("local") }
func (c *fakeZCopyConn) RemoteAddr() net.Addr { return fakeAddr("remote") }

func (c *fakeZCopyConn) RecvBorrowedMessage(_ context.Context) (*awsrdmahttp.BorrowedMessage, error) {
	if c.borrowErr != nil {
		return nil, c.borrowErr
	}
	if c.borrowed == nil {
		return nil, net.ErrClosed
	}
	msg := c.borrowed
	c.borrowed = nil
	return msg, nil
}

func (c *fakeZCopyConn) SendMessageAt(_ context.Context, payload []byte, sharedOffset int) error {
	c.sentAt = append(c.sentAt, sentAtRecord{
		payload: append([]byte(nil), payload...),
		offset:  sharedOffset,
	})
	return nil
}

func TestServiceHandlePutRespondsOKAndDoesNotStoreObject(t *testing.T) {
	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	conn := &fakeZCopyConn{}
	conn.borrowed = &awsrdmahttp.BorrowedMessage{
		Payload:      []byte("payload-by-rdma"),
		SharedOffset: 512,
	}
	zc, err := newZCopyConn(conn)
	if err != nil {
		t.Fatalf("newZCopyConn: %v", err)
	}
	svc := &Service{
		store:         memStore,
		maxObjectSize: 1024,
	}
	offset := 512

	err = svc.handlePut(zc, zcopyproto.Message{
		Op:         zcopyproto.OpPutReq,
		ReqID:      1,
		Bucket:     "bench-bucket",
		Key:        "rdma-object",
		Size:       len("payload-by-rdma"),
		DataOffset: &offset,
	})
	if err != nil {
		t.Fatalf("handlePut: %v", err)
	}

	if _, ok := memStore.GetObject("bench-bucket", "rdma-object"); ok {
		t.Fatal("unexpected stored object after handlePut")
	}
	if len(conn.sentControl) != 1 {
		t.Fatalf("control message count = %d, want 1", len(conn.sentControl))
	}
	msg, err := zcopyproto.Decode(conn.sentControl[0])
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if msg.Op != zcopyproto.OpRespOK {
		t.Fatalf("response op = %q, want %q", msg.Op, zcopyproto.OpRespOK)
	}
}

func TestServiceHandlePutDoesNotOverwritePreloadedObject(t *testing.T) {
	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	if _, err := memStore.PutObject("bench-bucket", "rdma-object", []byte("payload-from-loader")); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	conn := &fakeZCopyConn{}
	conn.borrowed = &awsrdmahttp.BorrowedMessage{
		Payload:      []byte("payload-by-rdma"),
		SharedOffset: 512,
	}
	zc, err := newZCopyConn(conn)
	if err != nil {
		t.Fatalf("newZCopyConn: %v", err)
	}
	svc := &Service{
		store:         memStore,
		maxObjectSize: 1024,
	}
	offset := 512

	err = svc.handlePut(zc, zcopyproto.Message{
		Op:         zcopyproto.OpPutReq,
		ReqID:      2,
		Bucket:     "bench-bucket",
		Key:        "rdma-object",
		Size:       len("payload-by-rdma"),
		DataOffset: &offset,
	})
	if err != nil {
		t.Fatalf("handlePut: %v", err)
	}

	obj, ok := memStore.GetObject("bench-bucket", "rdma-object")
	if !ok {
		t.Fatal("preloaded object missing after handlePut")
	}
	if got, want := string(obj.Body), "payload-from-loader"; got != want {
		t.Fatalf("stored body = %q, want %q", got, want)
	}
}

func TestServiceHandleGetSendsMetaAndPayload(t *testing.T) {
	memStore := store.NewMemoryStore(0, store.EvictPolicyReject)
	if _, err := memStore.PutObject("bench-bucket", "rdma-object", []byte("payload-by-rdma")); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	conn := &fakeZCopyConn{}
	zc, err := newZCopyConn(conn)
	if err != nil {
		t.Fatalf("newZCopyConn: %v", err)
	}
	zc.setCredits(1)

	svc := &Service{store: memStore}
	offset := 2048
	err = svc.handleGet(zc, zcopyproto.Message{
		Op:         zcopyproto.OpGetReq,
		ReqID:      99,
		Bucket:     "bench-bucket",
		Key:        "rdma-object",
		Max:        1024,
		DataOffset: &offset,
	})
	if err != nil {
		t.Fatalf("handleGet: %v", err)
	}

	if len(conn.sentControl) != 1 {
		t.Fatalf("control message count = %d, want 1", len(conn.sentControl))
	}
	meta, err := zcopyproto.Decode(conn.sentControl[0])
	if err != nil {
		t.Fatalf("decode get meta: %v", err)
	}
	if meta.Op != zcopyproto.OpGetMeta {
		t.Fatalf("meta op = %q, want %q", meta.Op, zcopyproto.OpGetMeta)
	}
	if meta.Size != len("payload-by-rdma") {
		t.Fatalf("meta size = %d, want %d", meta.Size, len("payload-by-rdma"))
	}
	if meta.DataOffset == nil || *meta.DataOffset != offset {
		t.Fatalf("meta offset = %v, want %d", meta.DataOffset, offset)
	}

	if len(conn.sentAt) != 1 {
		t.Fatalf("payload send count = %d, want 1", len(conn.sentAt))
	}
	if got, want := string(conn.sentAt[0].payload), "payload-by-rdma"; got != want {
		t.Fatalf("payload sent = %q, want %q", got, want)
	}
	if got, want := conn.sentAt[0].offset, offset; got != want {
		t.Fatalf("payload offset = %d, want %d", got, want)
	}
}
