package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	awsrdmahttp "github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma"
	log "github.com/sirupsen/logrus"

	"rdma-demo/server-client-demo/internal/s3rdmaserver/s3api"
	"rdma-demo/server-client-demo/internal/s3rdmaserver/store"
	"rdma-demo/server-client-demo/internal/s3rdmaserver/zcopy"
)

// Config owns the standalone s3-rdma-server runtime inputs after CLI parsing.
type Config struct {
	Debug            bool
	TCPListen        string
	EnableRDMAZCopy  bool
	RDMAZCopyListen  string
	RDMABacklog      int
	RDMAWorkers      int
	RDMALowCPU       bool
	RDMAFramePayload int
	RDMASendDepth    int
	RDMARecvDepth    int
	RDMAInline       int
	RDMASignalIntvl  int
	Region           string
	PayloadRoot      string
	MaxObjectSize    int64
}

type runningHTTPServer struct {
	name     string
	listener net.Listener
	server   *http.Server
}

func (c Config) normalized() Config {
	c.TCPListen = strings.TrimSpace(c.TCPListen)
	c.RDMAZCopyListen = strings.TrimSpace(c.RDMAZCopyListen)
	c.Region = strings.TrimSpace(c.Region)
	c.PayloadRoot = strings.TrimSpace(c.PayloadRoot)
	return c
}

func (c Config) Validate() error {
	c = c.normalized()

	if c.TCPListen == "" && !c.EnableRDMAZCopy {
		return fmt.Errorf("at least one data listener must be enabled via --tcp-listen or --enable-rdma-zcopy")
	}
	if c.Region == "" {
		return fmt.Errorf("region must not be empty")
	}
	if c.EnableRDMAZCopy && c.RDMAZCopyListen == "" {
		return fmt.Errorf("rdma-zcopy-listen must not be empty when --enable-rdma-zcopy is set")
	}
	if c.RDMABacklog < 0 {
		return fmt.Errorf("rdma-backlog must be >= 0")
	}
	if c.RDMAWorkers < 0 {
		return fmt.Errorf("rdma-accept-workers must be >= 0")
	}
	if c.RDMAFramePayload < 0 {
		return fmt.Errorf("rdma-frame-payload must be >= 0")
	}
	if c.RDMASendDepth < 0 {
		return fmt.Errorf("rdma-send-depth must be >= 0")
	}
	if c.RDMARecvDepth < 0 {
		return fmt.Errorf("rdma-recv-depth must be >= 0")
	}
	if c.RDMAInline < 0 {
		return fmt.Errorf("rdma-inline-threshold must be >= 0")
	}
	if c.RDMASignalIntvl < 0 {
		return fmt.Errorf("rdma-send-signal-interval must be >= 0")
	}
	return nil
}

func Run(cfg Config) error {
	cfg = cfg.normalized()
	if err := cfg.Validate(); err != nil {
		return err
	}

	memStore := store.NewMemoryStore()
	if cfg.PayloadRoot != "" {
		result, err := store.NewLoader(memStore).LoadRoot(cfg.PayloadRoot)
		if err != nil {
			return fmt.Errorf("preload payload root %s: %w", cfg.PayloadRoot, err)
		}
		log.WithFields(log.Fields{
			"payload_root":    cfg.PayloadRoot,
			"buckets_loaded":  result.BucketsLoaded,
			"objects_loaded":  result.ObjectsLoaded,
			"bytes_loaded":    result.BytesLoaded,
			"loaded_buckets":  result.Buckets,
			"max_object_size": cfg.MaxObjectSize,
		}).Info("payload root loaded")
	}

	log.WithFields(log.Fields{
		"tcp_listen":           cfg.TCPListen,
		"enable_rdma_zcopy":    cfg.EnableRDMAZCopy,
		"rdma_zcopy_listen":    cfg.RDMAZCopyListen,
		"region":               cfg.Region,
		"payload_root":         cfg.PayloadRoot,
		"max_object_size":      cfg.MaxObjectSize,
		"rdma_backlog":         cfg.RDMABacklog,
		"rdma_accept_workers":  cfg.RDMAWorkers,
		"rdma_frame_payload":   cfg.RDMAFramePayload,
		"rdma_send_depth":      cfg.RDMASendDepth,
		"rdma_recv_depth":      cfg.RDMARecvDepth,
		"rdma_inline":          cfg.RDMAInline,
		"rdma_lowcpu":          cfg.RDMALowCPU,
		"rdma_signal_interval": cfg.RDMASignalIntvl,
	}).Info("starting s3-rdma-server")

	handler := s3api.NewHandler(memStore, cfg.Region, cfg.MaxObjectSize)

	var httpServers []*runningHTTPServer
	var zcopySrv *zcopy.Service
	cleanupSetup := func() {
		if zcopySrv != nil {
			_ = zcopySrv.Close()
		}
		for _, srv := range httpServers {
			_ = srv.listener.Close()
		}
	}

	if cfg.TCPListen != "" {
		listener, err := net.Listen("tcp", cfg.TCPListen)
		if err != nil {
			return fmt.Errorf("listen tcp %s: %w", cfg.TCPListen, err)
		}
		httpServers = append(httpServers, &runningHTTPServer{
			name:     "tcp",
			listener: listener,
			server: &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			},
		})
		log.WithFields(log.Fields{
			"transport": "tcp",
			"listen":    listener.Addr().String(),
		}).Info("listener ready")
	}

	if cfg.EnableRDMAZCopy {
		msgListener, err := awsrdmahttp.NewVerbsMessageListener("rdma", cfg.RDMAZCopyListen, awsrdmahttp.VerbsListenerOptions{
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
			cleanupSetup()
			return fmt.Errorf("listen rdma zcopy %s: %w", cfg.RDMAZCopyListen, err)
		}
		zcopySrv = zcopy.NewService(msgListener, memStore, cfg.MaxObjectSize)
		log.WithFields(log.Fields{
			"transport": "rdma-zcopy",
			"listen":    msgListener.Addr().String(),
		}).Info("listener ready")
	}

	errCh := make(chan error, len(httpServers)+1)
	for _, srv := range httpServers {
		srv := srv
		go func() {
			if err := srv.server.Serve(srv.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s serve: %w", srv.name, err)
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var runErr error
	switch {
	case len(httpServers) == 0 && zcopySrv == nil:
		runErr = fmt.Errorf("no listener started")
	case true:
		select {
		case sig := <-sigCh:
			log.WithField("signal", sig.String()).Info("shutdown requested")
		case err := <-errCh:
			runErr = err
			log.WithError(err).Error("listener exited with error")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var shutdownErr error
	if zcopySrv != nil {
		if err := zcopySrv.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("rdma-zcopy shutdown: %w", err))
		}
	}
	for _, srv := range httpServers {
		if err := srv.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("%s shutdown: %w", srv.name, err))
		}
	}

	if shutdownErr != nil {
		log.WithError(shutdownErr).Error("shutdown finished with errors")
	}
	return errors.Join(runErr, shutdownErr)
}
