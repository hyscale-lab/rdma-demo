package zcopy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	awsrdmahttp "github.com/hyscale-lab/rdma-demo/rdma"
	"github.com/hyscale-lab/rdma-demo/rdma/zcopyproto"
	log "github.com/sirupsen/logrus"

	"github.com/hyscale-lab/rdma-demo/internal/s3rdmaserver/store"
)

// ObjectStore is the minimal readable state the zcopy service needs.
type ObjectStore interface {
	CreateBucket(name string)
	GetObject(bucket, key string) (store.Object, bool)
}

// Service serves the small zcopy control protocol expected by s3rdmaclient.
type Service struct {
	listener      awsrdmahttp.MessageListener
	store         ObjectStore
	maxObjectSize int64
}

func NewService(listener awsrdmahttp.MessageListener, store ObjectStore, maxObjectSize int64) *Service {
	return &Service{
		listener:      listener,
		store:         store,
		maxObjectSize: maxObjectSize,
	}
}

func (s *Service) Serve() error {
	if s.listener == nil {
		return fmt.Errorf("zcopy: listener is nil")
	}
	if s.store == nil {
		return fmt.Errorf("zcopy: store is nil")
	}

	for {
		conn, err := s.listener.AcceptMessage()
		if err != nil {
			return err
		}
		go s.serveConn(conn)
	}
}

func (s *Service) Close() error {
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

type offsetSender interface {
	SendMessageAt(ctx context.Context, payload []byte, sharedOffset int) error
}

type zcopyConn struct {
	conn      awsrdmahttp.MessageConn
	borrow    awsrdmahttp.BorrowingMessageConn
	offsetOut offsetSender

	sendMu sync.Mutex

	creditMu sync.Mutex
	creditCV *sync.Cond
	credits  int
	closed   bool

	wg sync.WaitGroup
}

func newZCopyConn(conn awsrdmahttp.MessageConn) (*zcopyConn, error) {
	zc := &zcopyConn{conn: conn}

	borrow, ok := conn.(awsrdmahttp.BorrowingMessageConn)
	if !ok {
		return nil, fmt.Errorf("zcopy: transport does not support borrowed receive")
	}
	offsetOut, ok := conn.(offsetSender)
	if !ok {
		return nil, fmt.Errorf("zcopy: transport does not support offset send")
	}

	zc.borrow = borrow
	zc.offsetOut = offsetOut
	zc.creditCV = sync.NewCond(&zc.creditMu)
	return zc, nil
}

func (zc *zcopyConn) close() {
	zc.creditMu.Lock()
	zc.closed = true
	zc.creditCV.Broadcast()
	zc.creditMu.Unlock()
}

func (zc *zcopyConn) addCredits(n int) {
	if n <= 0 {
		return
	}
	zc.creditMu.Lock()
	zc.credits += n
	zc.creditCV.Broadcast()
	zc.creditMu.Unlock()
}

func (zc *zcopyConn) setCredits(n int) {
	if n < 1 {
		n = 1
	}
	zc.creditMu.Lock()
	zc.credits = n
	zc.creditCV.Broadcast()
	zc.creditMu.Unlock()
}

func (zc *zcopyConn) waitCredit() error {
	zc.creditMu.Lock()
	defer zc.creditMu.Unlock()

	for zc.credits <= 0 && !zc.closed {
		zc.creditCV.Wait()
	}
	if zc.credits > 0 {
		zc.credits--
		return nil
	}
	return net.ErrClosed
}

func (zc *zcopyConn) sendControl(msg zcopyproto.Message) error {
	payload, err := zcopyproto.Encode(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return zc.conn.SendMessage(ctx, payload)
}

func (zc *zcopyConn) sendControlLocked(msg zcopyproto.Message) error {
	zc.sendMu.Lock()
	defer zc.sendMu.Unlock()
	return zc.sendControl(msg)
}

func (zc *zcopyConn) sendRespOK(reqID uint64) error {
	return zc.sendControlLocked(zcopyproto.Message{
		Op:    zcopyproto.OpRespOK,
		ReqID: reqID,
	})
}

func (zc *zcopyConn) sendRespErr(reqID uint64, errMsg string) error {
	return zc.sendControlLocked(zcopyproto.Message{
		Op:    zcopyproto.OpRespErr,
		ReqID: reqID,
		Err:   errMsg,
	})
}

func (zc *zcopyConn) sendGetPayload(reqID uint64, payload []byte, dataOffset int) error {
	if err := zc.waitCredit(); err != nil {
		return err
	}
	if dataOffset < 0 {
		return fmt.Errorf("zcopy: invalid data_offset=%d", dataOffset)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	zc.sendMu.Lock()
	defer zc.sendMu.Unlock()

	if err := zc.sendControl(zcopyproto.Message{
		Op:         zcopyproto.OpGetMeta,
		ReqID:      reqID,
		Size:       len(payload),
		DataOffset: &dataOffset,
	}); err != nil {
		return err
	}
	return zc.offsetOut.SendMessageAt(ctx, payload, dataOffset)
}

func (zc *zcopyConn) recvControl() (zcopyproto.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	payload, err := zc.conn.RecvMessage(ctx)
	if err != nil {
		return zcopyproto.Message{}, err
	}
	return zcopyproto.Decode(payload)
}

func (zc *zcopyConn) recvPayloadAt(size int, sharedOffset int) error {
	if size < 0 {
		return fmt.Errorf("zcopy: invalid payload size %d", size)
	}
	if sharedOffset < 0 {
		return fmt.Errorf("zcopy: invalid data_offset=%d", sharedOffset)
	}
	if size == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bmsg, err := zc.borrow.RecvBorrowedMessage(ctx)
	if err != nil {
		return err
	}
	defer bmsg.Release()

	if bmsg.SharedOffset != sharedOffset {
		return fmt.Errorf("zcopy: payload offset mismatch got=%d want=%d", bmsg.SharedOffset, sharedOffset)
	}
	if len(bmsg.Payload) != size {
		return fmt.Errorf("zcopy: payload size mismatch got=%d want=%d", len(bmsg.Payload), size)
	}
	return nil
}

func (s *Service) serveConn(conn awsrdmahttp.MessageConn) {
	zc, err := newZCopyConn(conn)
	if err != nil {
		log.WithError(err).Error("s3-rdma-server zcopy init failed")
		_ = conn.Close()
		return
	}

	defer func() {
		zc.close()
		zc.wg.Wait()
		_ = conn.Close()
	}()

	for {
		msg, err := zc.recvControl()
		if err != nil {
			return
		}
		log.Debugf("zcopy received control message: %+v", msg)

		switch msg.Op {
		case zcopyproto.OpHelloReq:
			if msg.ReqID == 0 {
				_ = zc.sendRespErr(0, "missing req_id")
				continue
			}
			if msg.Credits <= 0 {
				_ = zc.sendControlLocked(zcopyproto.Message{
					Op:    zcopyproto.OpHelloResp,
					ReqID: msg.ReqID,
					Err:   "invalid credits",
				})
				continue
			}
			zc.setCredits(msg.Credits)
			if err := zc.sendControlLocked(zcopyproto.Message{
				Op:      zcopyproto.OpHelloResp,
				ReqID:   msg.ReqID,
				Credits: msg.Credits,
			}); err != nil {
				return
			}
		case zcopyproto.OpAck:
			zc.addCredits(msg.Ack)
		case zcopyproto.OpEnsureBucketReq:
			if msg.ReqID == 0 {
				_ = zc.sendRespErr(0, "missing req_id")
				continue
			}
			bucket := strings.TrimSpace(msg.Bucket)
			if bucket == "" {
				_ = zc.sendRespErr(msg.ReqID, "bucket must not be empty")
				continue
			}
			s.store.CreateBucket(bucket)
			if err := zc.sendRespOK(msg.ReqID); err != nil {
				return
			}
		case zcopyproto.OpPutReq:
			if err := s.handlePut(zc, msg); err != nil {
				log.WithError(err).Error("s3-rdma-server zcopy put failed")
				return
			}
		case zcopyproto.OpGetReq:
			if err := s.handleGetAsync(zc, msg); err != nil {
				log.WithError(err).Error("s3-rdma-server zcopy get schedule failed")
				return
			}
		default:
			if msg.ReqID != 0 {
				if err := zc.sendRespErr(msg.ReqID, "unsupported op"); err != nil {
					return
				}
			}
		}
	}
}

func (s *Service) handlePut(zc *zcopyConn, msg zcopyproto.Message) error {
	if s.store == nil {
		return fmt.Errorf("zcopy: store is nil")
	}
	if msg.ReqID == 0 {
		return zc.sendRespErr(0, "missing req_id")
	}

	bucket := strings.TrimSpace(msg.Bucket)
	key := strings.TrimSpace(msg.Key)
	if bucket == "" || key == "" {
		return zc.sendRespErr(msg.ReqID, "bucket/key must not be empty")
	}
	if msg.Size < 0 {
		return zc.sendRespErr(msg.ReqID, "invalid put size")
	}
	if msg.DataOffset == nil {
		return zc.sendRespErr(msg.ReqID, "missing data_offset")
	}
	if s.maxObjectSize > 0 && int64(msg.Size) > s.maxObjectSize {
		return zc.sendRespErr(msg.ReqID, fmt.Sprintf("object too large: %d > %d", msg.Size, s.maxObjectSize))
	}

	if err := zc.recvPayloadAt(msg.Size, *msg.DataOffset); err != nil {
		return err
	}
	s.store.CreateBucket(bucket)
	return zc.sendRespOK(msg.ReqID)
}

func (s *Service) handleGetAsync(zc *zcopyConn, msg zcopyproto.Message) error {
	if msg.ReqID == 0 {
		return zc.sendRespErr(0, "missing req_id")
	}

	req := msg
	zc.wg.Add(1)
	go func() {
		defer zc.wg.Done()
		if err := s.handleGet(zc, req); err != nil && !errors.Is(err, net.ErrClosed) {
			log.WithError(err).WithField("req_id", req.ReqID).Error("s3-rdma-server zcopy get failed")
		}
	}()
	return nil
}

func (s *Service) handleGet(zc *zcopyConn, msg zcopyproto.Message) error {
	if s.store == nil {
		return fmt.Errorf("zcopy: store is nil")
	}

	bucket := strings.TrimSpace(msg.Bucket)
	key := strings.TrimSpace(msg.Key)
	if bucket == "" || key == "" {
		return zc.sendRespErr(msg.ReqID, "bucket/key must not be empty")
	}
	if msg.Max < 0 {
		return zc.sendRespErr(msg.ReqID, "invalid max size")
	}
	if msg.DataOffset == nil {
		return zc.sendRespErr(msg.ReqID, "missing data_offset")
	}

	obj, ok := s.store.GetObject(bucket, key)
	if !ok {
		return zc.sendRespErr(msg.ReqID, "key not found")
	}
	if msg.Max > 0 && len(obj.Body) > msg.Max {
		return zc.sendRespErr(msg.ReqID, fmt.Sprintf("object exceeds max=%d size=%d", msg.Max, len(obj.Body)))
	}

	return zc.sendGetPayload(msg.ReqID, obj.Body, *msg.DataOffset)
}
