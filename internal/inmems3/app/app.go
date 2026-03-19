package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsrdmahttp "github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma"
	"google.golang.org/grpc"

	"rdma-demo/server-client-demo/internal/inmems3/control"
	"rdma-demo/server-client-demo/internal/inmems3/payload"
	"rdma-demo/server-client-demo/internal/inmems3/s3api"
	"rdma-demo/server-client-demo/internal/inmems3/store"
	"rdma-demo/server-client-demo/internal/inmems3/zcopy"
)

type Config struct {
	TCPListen         string
	EnableRDMAZCopy   bool
	RDMAZCopyListen   string
	RDMABacklog       int
	RDMAWorkers       int
	RDMALowCPU        bool
	RDMAFramePayload  int
	RDMASendDepth     int
	RDMARecvDepth     int
	RDMAInline        int
	RDMASignalIntvl   int
	Region            string
	MaxObjectSize     int64
	StoreMaxBytes     int64
	StoreEvictPolicy  string
	PayloadRoot       string
	ControlGRPCListen string
}

type runningServer struct {
	name string
	ln   net.Listener
	srv  *http.Server
}

func DefaultConfig() Config {
	return Config{
		TCPListen:        "127.0.0.1:10090",
		RDMAZCopyListen:  "127.0.0.1:10191",
		RDMABacklog:      awsrdmahttp.DefaultVerbsListenBacklog,
		RDMAWorkers:      awsrdmahttp.DefaultVerbsAcceptWorkers,
		Region:           "us-east-1",
		MaxObjectSize:    64 << 20,
		StoreEvictPolicy: string(store.EvictPolicyReject),
	}
}

func (c *Config) RegisterFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.TCPListen, "tcp-listen", c.TCPListen, "TCP listen address, empty disables TCP")
	fs.BoolVar(&c.EnableRDMAZCopy, "enable-rdma-zcopy", c.EnableRDMAZCopy, "enable RDMA zcopy protocol listener")
	fs.StringVar(&c.RDMAZCopyListen, "rdma-zcopy-listen", c.RDMAZCopyListen, "RDMA zcopy listen address")
	fs.IntVar(&c.RDMABacklog, "rdma-backlog", c.RDMABacklog, "RDMA listen backlog")
	fs.IntVar(&c.RDMAWorkers, "rdma-accept-workers", c.RDMAWorkers, "RDMA accept worker count")
	fs.BoolVar(&c.RDMALowCPU, "rdma-lowcpu", c.RDMALowCPU, "use RDMA low-cpu mode")
	fs.IntVar(&c.RDMAFramePayload, "rdma-frame-payload", c.RDMAFramePayload, "RDMA frame payload size in bytes (0=default)")
	fs.IntVar(&c.RDMASendDepth, "rdma-send-depth", c.RDMASendDepth, "RDMA send queue depth (0=default)")
	fs.IntVar(&c.RDMARecvDepth, "rdma-recv-depth", c.RDMARecvDepth, "RDMA recv queue depth (0=default)")
	fs.IntVar(&c.RDMAInline, "rdma-inline-threshold", c.RDMAInline, "RDMA inline threshold in bytes (0=default)")
	fs.IntVar(&c.RDMASignalIntvl, "rdma-send-signal-interval", c.RDMASignalIntvl, "RDMA send signal interval (0=default)")
	fs.StringVar(&c.Region, "region", c.Region, "region returned by server")
	fs.Int64Var(&c.MaxObjectSize, "max-object-size", c.MaxObjectSize, "max object size in bytes, <=0 means unlimited")
	fs.Int64Var(&c.StoreMaxBytes, "store-max-bytes", c.StoreMaxBytes, "max total in-memory bytes for stored objects, <=0 means unlimited")
	fs.StringVar(&c.StoreEvictPolicy, "store-evict-policy", c.StoreEvictPolicy, "store eviction policy when full: reject or fifo")
	fs.StringVar(&c.PayloadRoot, "payload-root", c.PayloadRoot, "optional root directory to preload as bucket/object payloads")
	fs.StringVar(&c.ControlGRPCListen, "control-grpc-listen", c.ControlGRPCListen, "optional gRPC control-plane listen address")
}

func Run(cfg Config) error {
	if cfg.TCPListen == "" && !cfg.EnableRDMAZCopy {
		return fmt.Errorf("at least one listener must be enabled")
	}
	if cfg.StoreMaxBytes < 0 {
		return fmt.Errorf("store-max-bytes must be >= 0")
	}

	evictPolicy, err := store.ParseEvictPolicy(cfg.StoreEvictPolicy)
	if err != nil {
		return err
	}

	memStore := store.NewMemoryStore(cfg.StoreMaxBytes, evictPolicy)
	payloadLoader := payload.NewLoader(memStore)
	if cfg.PayloadRoot != "" {
		result, err := payloadLoader.AddFolder(context.Background(), cfg.PayloadRoot)
		if err != nil {
			return fmt.Errorf("preload payload root %s: %w", cfg.PayloadRoot, err)
		}
		log.Printf("payload-root preloaded path=%s buckets=%d objects=%d bytes=%d", cfg.PayloadRoot, result.BucketsLoaded, result.ObjectsLoaded, result.BytesLoaded)
	}
	handler := s3api.NewHandler(memStore, cfg.Region, cfg.MaxObjectSize)

	var servers []*runningServer
	if cfg.TCPListen != "" {
		ln, err := net.Listen("tcp", cfg.TCPListen)
		if err != nil {
			return fmt.Errorf("listen tcp %s: %w", cfg.TCPListen, err)
		}
		servers = append(servers, &runningServer{
			name: "tcp",
			ln:   ln,
			srv: &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			},
		})
	}

	var zcopySrv *zcopy.Service
	if cfg.EnableRDMAZCopy {
		msgLn, err := awsrdmahttp.NewVerbsMessageListener("rdma", cfg.RDMAZCopyListen, awsrdmahttp.VerbsListenerOptions{
			VerbsOptions: awsrdmahttp.VerbsOptions{
				FramePayloadSize:   cfg.RDMAFramePayload,
				SendQueueDepth:     cfg.RDMASendDepth,
				RecvQueueDepth:     cfg.RDMARecvDepth,
				InlineThreshold:    cfg.RDMAInline,
				LowCPU:             cfg.RDMALowCPU,
				SendSignalInterval: cfg.RDMASignalIntvl,
			},
			Backlog:       cfg.RDMABacklog,
			AcceptWorkers: cfg.RDMAWorkers,
		})
		if err != nil {
			return fmt.Errorf("listen rdma zcopy %s: %w", cfg.RDMAZCopyListen, err)
		}
		zcopySrv = zcopy.NewService(msgLn, memStore, cfg.MaxObjectSize)
		log.Printf("rdma-zcopy listening on %s", msgLn.Addr())
	}
	var (
		controlLn  net.Listener
		controlSrv *grpc.Server
	)
	if cfg.ControlGRPCListen != "" {
		ln, err := net.Listen("tcp", cfg.ControlGRPCListen)
		if err != nil {
			return fmt.Errorf("listen control grpc %s: %w", cfg.ControlGRPCListen, err)
		}
		controlLn = ln
		controlSrv = grpc.NewServer()
		control.RegisterGRPC(controlSrv, payloadLoader)
		log.Printf("control-grpc listening on %s", controlLn.Addr())
	}
	if len(servers) == 0 && zcopySrv == nil && controlSrv == nil {
		return fmt.Errorf("no listener started")
	}

	errCh := make(chan error, len(servers)+2)
	for _, rs := range servers {
		rs := rs
		log.Printf("%s listening on %s", rs.name, rs.ln.Addr())
		go func() {
			if err := rs.srv.Serve(rs.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s serve: %w", rs.name, err)
			}
		}()
	}
	if zcopySrv != nil {
		go func() {
			if err := zcopySrv.Serve(); err != nil && !errors.Is(err, net.ErrClosed) {
				errCh <- fmt.Errorf("rdma-zcopy serve: %w", err)
			}
		}()
	}
	if controlSrv != nil {
		go func() {
			if err := controlSrv.Serve(controlLn); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				errCh <- fmt.Errorf("control-grpc serve: %w", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var shutdownReason string
	select {
	case sig := <-sigCh:
		shutdownReason = fmt.Sprintf("signal=%s", sig.String())
	case err := <-errCh:
		shutdownReason = fmt.Sprintf("serve_error=%v", err)
	}
	log.Printf("shutting down: %s", shutdownReason)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if zcopySrv != nil {
		if err := zcopySrv.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("rdma-zcopy shutdown: %v", err)
		}
	}
	if controlSrv != nil {
		controlSrv.GracefulStop()
	}
	for _, rs := range servers {
		if err := rs.srv.Shutdown(ctx); err != nil {
			log.Printf("%s shutdown: %v", rs.name, err)
		}
	}

	return nil
}
