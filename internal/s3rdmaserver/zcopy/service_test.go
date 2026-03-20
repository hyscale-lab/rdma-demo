package zcopy

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	awsrdmahttp "github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma"
	"github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma/zcopyproto"

	"rdma-demo/server-client-demo/internal/s3rdmaserver/store"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "test" }
func (a fakeAddr) String() string  { return string(a) }

type sentAtRecord struct {
	payload []byte
	offset  int
}

type fakeZCopyConn struct {
	mu             sync.Mutex
	borrowedMsgs   []*awsrdmahttp.BorrowedMessage
	borrowErr      error
	sentControl    [][]byte
	sentAt         []sentAtRecord
	closeCount     atomic.Int32
	recvMsgPayload [][]byte
	waitForSentAt  int
}

func (c *fakeZCopyConn) SendMessage(_ context.Context, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sentControl = append(c.sentControl, append([]byte(nil), payload...))
	return nil
}

func (c *fakeZCopyConn) RecvMessage(_ context.Context) ([]byte, error) {
	deadline := time.Now().Add(50 * time.Millisecond)
	for {
		c.mu.Lock()
		if len(c.recvMsgPayload) > 0 {
			payload := append([]byte(nil), c.recvMsgPayload[0]...)
			c.recvMsgPayload = c.recvMsgPayload[1:]
			c.mu.Unlock()
			return payload, nil
		}
		waitForSentAt := c.waitForSentAt
		sentAtCount := len(c.sentAt)
		c.mu.Unlock()

		if waitForSentAt <= 0 || sentAtCount >= waitForSentAt || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.borrowErr != nil {
		return nil, c.borrowErr
	}
	if len(c.borrowedMsgs) == 0 {
		return nil, net.ErrClosed
	}
	msg := c.borrowedMsgs[0]
	c.borrowedMsgs = c.borrowedMsgs[1:]
	return msg, nil
}

func (c *fakeZCopyConn) SendMessageAt(_ context.Context, payload []byte, sharedOffset int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sentAt = append(c.sentAt, sentAtRecord{
		payload: append([]byte(nil), payload...),
		offset:  sharedOffset,
	})
	return nil
}

func TestServiceHandlePutRespondsOKAndDoesNotStoreObject(t *testing.T) {
	memStore := store.NewMemoryStore()
	conn := &fakeZCopyConn{
		waitForSentAt: 1,
		borrowedMsgs: []*awsrdmahttp.BorrowedMessage{
			{
				Payload:      []byte("payload-by-rdma"),
				SharedOffset: 512,
			},
		},
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

	if !memStore.BucketExists("bench-bucket") {
		t.Fatal("bucket was not created by handlePut")
	}
	if _, ok := memStore.GetObject("bench-bucket", "rdma-object"); ok {
		t.Fatal("unexpected stored object after handlePut")
	}
	if len(conn.sentControl) != 1 {
		t.Fatalf("control message count = %d, want 1", len(conn.sentControl))
	}

	msg := decodeControl(t, conn.sentControl[0])
	if msg.Op != zcopyproto.OpRespOK {
		t.Fatalf("response op = %q, want %q", msg.Op, zcopyproto.OpRespOK)
	}
}

func TestServiceHandlePutDoesNotOverwritePreloadedObject(t *testing.T) {
	memStore := store.NewMemoryStore()
	memStore.PutLoadedObject("bench-bucket", "rdma-object", []byte("payload-from-loader"), time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC))

	conn := &fakeZCopyConn{
		borrowedMsgs: []*awsrdmahttp.BorrowedMessage{
			{
				Payload:      []byte("payload-by-rdma"),
				SharedOffset: 512,
			},
		},
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
	memStore := store.NewMemoryStore()
	memStore.PutLoadedObject("bench-bucket", "rdma-object", []byte("payload-by-rdma"), time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC))

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
	meta := decodeControl(t, conn.sentControl[0])
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

func TestServiceServeConnHelloEnsureBucketPutAndGet(t *testing.T) {
	memStore := store.NewMemoryStore()
	memStore.PutLoadedObject("payload-bucket", "preloaded.txt", []byte("payload-from-store"), time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC))

	putOffset := 256
	getOffset := 1024
	conn := &fakeZCopyConn{
		borrowedMsgs: []*awsrdmahttp.BorrowedMessage{
			{
				Payload:      []byte("payload-by-rdma"),
				SharedOffset: putOffset,
			},
		},
		recvMsgPayload: [][]byte{
			mustEncodeMessage(t, zcopyproto.Message{
				Op:      zcopyproto.OpHelloReq,
				ReqID:   1,
				Credits: 2,
			}),
			mustEncodeMessage(t, zcopyproto.Message{
				Op:     zcopyproto.OpEnsureBucketReq,
				ReqID:  2,
				Bucket: "upload-bucket",
			}),
			mustEncodeMessage(t, zcopyproto.Message{
				Op:         zcopyproto.OpPutReq,
				ReqID:      3,
				Bucket:     "upload-bucket",
				Key:        "upload.bin",
				Size:       len("payload-by-rdma"),
				DataOffset: &putOffset,
			}),
			mustEncodeMessage(t, zcopyproto.Message{
				Op:         zcopyproto.OpGetReq,
				ReqID:      4,
				Bucket:     "payload-bucket",
				Key:        "preloaded.txt",
				Max:        4096,
				DataOffset: &getOffset,
			}),
		},
	}

	svc := &Service{
		store:         memStore,
		maxObjectSize: 1024,
	}
	svc.serveConn(conn)

	if got, want := conn.closeCount.Load(), int32(1); got != want {
		t.Fatalf("close count = %d, want %d", got, want)
	}
	if !memStore.BucketExists("upload-bucket") {
		t.Fatal("ensure bucket did not create upload-bucket")
	}
	if _, ok := memStore.GetObject("upload-bucket", "upload.bin"); ok {
		t.Fatal("uploaded object was unexpectedly retained")
	}

	if len(conn.sentControl) != 4 {
		t.Fatalf("control message count = %d, want 4", len(conn.sentControl))
	}
	helloResp := decodeControl(t, conn.sentControl[0])
	if helloResp.Op != zcopyproto.OpHelloResp || helloResp.Credits != 2 {
		t.Fatalf("hello response = %+v, want hello_resp with credits=2", helloResp)
	}
	if resp := decodeControl(t, conn.sentControl[1]); resp.Op != zcopyproto.OpRespOK || resp.ReqID != 2 {
		t.Fatalf("ensure bucket response = %+v, want resp_ok req=2", resp)
	}
	if resp := decodeControl(t, conn.sentControl[2]); resp.Op != zcopyproto.OpRespOK || resp.ReqID != 3 {
		t.Fatalf("put response = %+v, want resp_ok req=3", resp)
	}
	meta := decodeControl(t, conn.sentControl[3])
	if meta.Op != zcopyproto.OpGetMeta || meta.ReqID != 4 {
		t.Fatalf("get meta = %+v, want get_meta req=4", meta)
	}
	if meta.DataOffset == nil || *meta.DataOffset != getOffset {
		t.Fatalf("get meta offset = %v, want %d", meta.DataOffset, getOffset)
	}
	if meta.Size != len("payload-from-store") {
		t.Fatalf("get meta size = %d, want %d", meta.Size, len("payload-from-store"))
	}

	if len(conn.sentAt) != 1 {
		t.Fatalf("payload send count = %d, want 1", len(conn.sentAt))
	}
	if got, want := string(conn.sentAt[0].payload), "payload-from-store"; got != want {
		t.Fatalf("payload sent = %q, want %q", got, want)
	}
	if got, want := conn.sentAt[0].offset, getOffset; got != want {
		t.Fatalf("payload offset = %d, want %d", got, want)
	}
}

func mustEncodeMessage(t *testing.T, msg zcopyproto.Message) []byte {
	t.Helper()

	payload, err := zcopyproto.Encode(msg)
	if err != nil {
		t.Fatalf("encode control message: %v", err)
	}
	return payload
}

func decodeControl(t *testing.T, payload []byte) zcopyproto.Message {
	t.Helper()

	msg, err := zcopyproto.Decode(payload)
	if err != nil {
		t.Fatalf("decode control message: %v", err)
	}
	return msg
}
