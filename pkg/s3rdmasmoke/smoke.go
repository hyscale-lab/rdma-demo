package s3rdmasmoke

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	awsrdmahttp "github.com/hyscale-lab/rdma-demo/pkg/rdma"
	"github.com/hyscale-lab/rdma-demo/pkg/rdma/client"
)

const (
	defaultTransport         = "tcp"
	defaultTCPEndpoint       = "http://127.0.0.1:10090"
	defaultRDMAEndpoint      = "127.0.0.1:10191"
	defaultRegion            = "us-east-1"
	defaultAccessKey         = "test"
	defaultSecretKey         = "test"
	defaultRequestTimeout    = 30 * time.Second
	defaultRDMAConnectTO     = 10 * time.Second
	defaultRDMAControlTO     = 30 * time.Second
	defaultRDMADataTO        = 2 * time.Minute
	defaultRDMASharedMemSize = 16 << 20
	defaultRDMAGetOffset     = 8 << 20
	defaultRDMAGetMaxSize    = 8 << 20
	defaultPayloadSize       = 4096
	transportTCP             = "tcp"
	transportRDMA            = "rdma"
	opPut                    = "put"
	opGet                    = "get"
	opPutGet                 = "put-get"
)

// Config describes one small smoke run against the standalone benchmark server.
type Config struct {
	Transport       string
	Endpoint        string
	Bucket          string
	Key             string
	Op              string
	Verify          bool
	PayloadSize     int
	RequestTimeout  time.Duration
	RDMAConnectTO   time.Duration
	RDMAControlTO   time.Duration
	RDMADataTO      time.Duration
	RDMASharedMem   int
	RDMAPutOffset   int
	RDMAGetOffset   int
	RDMAGetMaxSize  int
	RDMALowCPU      bool
	RDMAFrameSize   int
	RDMASendDepth   int
	RDMARecvDepth   int
	RDMAInlineBytes int
	RDMASignalIntvl int
}

// Result captures the useful outcome of a smoke run.
type Result struct {
	Transport   string
	Endpoint    string
	Bucket      string
	Key         string
	Operation   string
	PutAccepted bool
	GetBytes    int
	GetMissing  bool
	Verified    bool
}

func (r Result) Summary() string {
	parts := []string{
		"smoke ok",
		"transport=" + r.Transport,
		"op=" + r.Operation,
		"endpoint=" + r.Endpoint,
		"bucket=" + r.Bucket,
		"key=" + r.Key,
	}
	if r.PutAccepted {
		parts = append(parts, "put_accepted=true")
	}
	if r.GetBytes > 0 {
		parts = append(parts, fmt.Sprintf("get_bytes=%d", r.GetBytes))
	}
	if r.GetMissing {
		parts = append(parts, "get_missing=true")
	}
	parts = append(parts, fmt.Sprintf("verified=%t", r.Verified))
	return strings.Join(parts, " ")
}

func (c Config) normalized() Config {
	c.Transport = strings.ToLower(strings.TrimSpace(c.Transport))
	if c.Transport == "" {
		c.Transport = defaultTransport
	}

	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if c.Endpoint == "" {
		switch c.Transport {
		case transportRDMA:
			c.Endpoint = defaultRDMAEndpoint
		default:
			c.Endpoint = defaultTCPEndpoint
		}
	}
	if c.Transport == transportTCP {
		c.Endpoint = normalizeTCPEndpoint(c.Endpoint)
	}

	c.Bucket = strings.TrimSpace(c.Bucket)
	c.Key = strings.TrimSpace(c.Key)
	c.Op = strings.ToLower(strings.TrimSpace(c.Op))
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultRequestTimeout
	}
	if c.PayloadSize == 0 {
		c.PayloadSize = defaultPayloadSize
	}
	if c.Transport == transportRDMA {
		if c.RDMAConnectTO <= 0 {
			c.RDMAConnectTO = defaultRDMAConnectTO
		}
		if c.RDMAControlTO <= 0 {
			c.RDMAControlTO = defaultRDMAControlTO
		}
		if c.RDMADataTO <= 0 {
			c.RDMADataTO = defaultRDMADataTO
		}
		if c.RDMASharedMem <= 0 {
			c.RDMASharedMem = defaultRDMASharedMemSize
		}
		if c.RDMAGetMaxSize <= 0 {
			c.RDMAGetMaxSize = defaultRDMAGetMaxSize
		}
		if c.RDMAGetOffset < 0 {
			c.RDMAGetOffset = defaultRDMAGetOffset
		}
	}
	return c
}

func (c Config) Validate() error {
	c = c.normalized()

	switch c.Transport {
	case transportTCP, transportRDMA:
	default:
		return fmt.Errorf("transport must be tcp or rdma")
	}
	switch c.Op {
	case opPut, opGet, opPutGet:
	default:
		return fmt.Errorf("op must be put, get, or put-get")
	}
	if c.Endpoint == "" {
		return fmt.Errorf("endpoint must not be empty")
	}
	if c.Bucket == "" {
		return fmt.Errorf("bucket must not be empty")
	}
	if c.Key == "" {
		return fmt.Errorf("key must not be empty")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("request-timeout must be > 0")
	}
	if c.PayloadSize < 0 {
		return fmt.Errorf("payload-size must be >= 0")
	}
	if c.Transport != transportRDMA {
		return nil
	}
	if c.RDMASharedMem <= 0 {
		return fmt.Errorf("rdma-shared-memory-size must be > 0")
	}
	if c.RDMAConnectTO <= 0 {
		return fmt.Errorf("rdma-connect-timeout must be > 0")
	}
	if c.RDMAControlTO <= 0 {
		return fmt.Errorf("rdma-control-timeout must be > 0")
	}
	if c.RDMADataTO <= 0 {
		return fmt.Errorf("rdma-data-timeout must be > 0")
	}
	if c.RDMAPutOffset < 0 {
		return fmt.Errorf("rdma-put-offset must be >= 0")
	}
	if c.RDMAGetOffset < 0 {
		return fmt.Errorf("rdma-get-offset must be >= 0")
	}
	if c.RDMAGetMaxSize <= 0 {
		return fmt.Errorf("rdma-get-max-size must be > 0")
	}
	if c.RDMAFrameSize < 0 {
		return fmt.Errorf("rdma-frame-payload must be >= 0")
	}
	if c.RDMASendDepth < 0 {
		return fmt.Errorf("rdma-send-depth must be >= 0")
	}
	if c.RDMARecvDepth < 0 {
		return fmt.Errorf("rdma-recv-depth must be >= 0")
	}
	if c.RDMAInlineBytes < 0 {
		return fmt.Errorf("rdma-inline-threshold must be >= 0")
	}
	if c.RDMASignalIntvl < 0 {
		return fmt.Errorf("rdma-send-signal-interval must be >= 0")
	}
	if c.RDMAPutOffset+c.PayloadSize > c.RDMASharedMem {
		return fmt.Errorf("rdma put range offset=%d size=%d exceeds shared memory size=%d", c.RDMAPutOffset, c.PayloadSize, c.RDMASharedMem)
	}
	if c.RDMAGetOffset+c.RDMAGetMaxSize > c.RDMASharedMem {
		return fmt.Errorf("rdma get range offset=%d max=%d exceeds shared memory size=%d", c.RDMAGetOffset, c.RDMAGetMaxSize, c.RDMASharedMem)
	}
	return nil
}

// Run executes one smoke run and validates the expected outcome.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg = cfg.normalized()
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}

	switch cfg.Transport {
	case transportTCP:
		return runTCP(ctx, cfg)
	case transportRDMA:
		return runRDMA(ctx, cfg)
	default:
		return Result{}, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

func runTCP(ctx context.Context, cfg Config) (Result, error) {
	client := s3.New(s3.Options{
		Region:       defaultRegion,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(defaultAccessKey, defaultSecretKey, ""),
	})

	result := Result{
		Transport: cfg.Transport,
		Endpoint:  cfg.Endpoint,
		Bucket:    cfg.Bucket,
		Key:       cfg.Key,
		Operation: cfg.Op,
	}

	switch cfg.Op {
	case opPut:
		if err := ensureBucketTCP(ctx, client, cfg); err != nil {
			return result, err
		}
		if err := putObjectTCP(ctx, client, cfg); err != nil {
			return result, err
		}
		result.PutAccepted = true
		result.Verified = true
		return result, nil
	case opGet:
		body, err := getObjectTCP(ctx, client, cfg)
		if err != nil {
			return result, err
		}
		result.GetBytes = len(body)
		if cfg.Verify && cfg.PayloadSize > 0 && len(body) != cfg.PayloadSize {
			return result, fmt.Errorf("tcp smoke: get size=%d want=%d", len(body), cfg.PayloadSize)
		}
		result.Verified = true
		return result, nil
	case opPutGet:
		if err := ensureBucketTCP(ctx, client, cfg); err != nil {
			return result, err
		}
		if err := putObjectTCP(ctx, client, cfg); err != nil {
			return result, err
		}
		result.PutAccepted = true
		_, err := getObjectTCP(ctx, client, cfg)
		if err == nil {
			return result, fmt.Errorf("tcp smoke: key %q remained readable after PUT; use a fresh key for put-get", cfg.Key)
		}
		if !isExpectedTCPSmokeMissing(err) {
			return result, err
		}
		result.GetMissing = true
		result.Verified = true
		return result, nil
	default:
		return result, fmt.Errorf("unsupported op %q", cfg.Op)
	}
}

func runRDMA(ctx context.Context, cfg Config) (result Result, err error) {
	shared, cleanup, err := newSharedMmap(cfg.RDMASharedMem)
	if err != nil {
		return Result{}, fmt.Errorf("rdma smoke: mmap shared memory: %w", err)
	}
	defer func() {
		err = errors.Join(err, cleanup())
	}()

	payload := shared[cfg.RDMAPutOffset : cfg.RDMAPutOffset+cfg.PayloadSize]
	fillPayload(payload)

	client, err := s3rdmaclient.New(s3rdmaclient.Config{
		Endpoint:       cfg.Endpoint,
		ConnectTimeout: cfg.RDMAConnectTO,
		ControlTimeout: cfg.RDMAControlTO,
		DataTimeout:    cfg.RDMADataTO,
		VerbsOptions: awsrdmahttp.VerbsOptions{
			FramePayloadSize:   cfg.RDMAFrameSize,
			SendQueueDepth:     cfg.RDMASendDepth,
			RecvQueueDepth:     cfg.RDMARecvDepth,
			InlineThreshold:    cfg.RDMAInlineBytes,
			LowCPU:             cfg.RDMALowCPU,
			SendSignalInterval: cfg.RDMASignalIntvl,
		},
	}, shared)
	if err != nil {
		return Result{}, fmt.Errorf("rdma smoke: init s3rdmaclient: %w", err)
	}
	defer func() {
		err = errors.Join(err, client.Close())
	}()

	result = Result{
		Transport: cfg.Transport,
		Endpoint:  cfg.Endpoint,
		Bucket:    cfg.Bucket,
		Key:       cfg.Key,
		Operation: cfg.Op,
	}

	switch cfg.Op {
	case opPut:
		if err := ensureBucketRDMA(ctx, client, cfg); err != nil {
			return result, err
		}
		if err := putObjectRDMA(ctx, client, cfg); err != nil {
			return result, err
		}
		result.PutAccepted = true
		result.Verified = true
		return result, nil
	case opGet:
		ref, err := getObjectRDMA(ctx, client, cfg)
		if err != nil {
			return result, err
		}
		defer func() {
			err = errors.Join(err, client.Release(ref))
		}()
		result.GetBytes = ref.Size
		if cfg.Verify && cfg.PayloadSize > 0 && ref.Size != cfg.PayloadSize {
			return result, fmt.Errorf("rdma smoke: get size=%d want=%d", ref.Size, cfg.PayloadSize)
		}
		result.Verified = true
		return result, nil
	case opPutGet:
		if err := ensureBucketRDMA(ctx, client, cfg); err != nil {
			return result, err
		}
		if err := putObjectRDMA(ctx, client, cfg); err != nil {
			return result, err
		}
		result.PutAccepted = true
		ref, err := getObjectRDMA(ctx, client, cfg)
		if err == nil {
			_ = client.Release(ref)
			return result, fmt.Errorf("rdma smoke: key %q remained readable after PUT; use a fresh key for put-get", cfg.Key)
		}
		if !isExpectedRDMASmokeMissing(err) {
			return result, err
		}
		result.GetMissing = true
		result.Verified = true
		return result, nil
	default:
		return result, fmt.Errorf("unsupported op %q", cfg.Op)
	}
}

func ensureBucketTCP(ctx context.Context, client *s3.Client, cfg Config) error {
	opCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()

	_, err := client.CreateBucket(opCtx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)})
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return nil
		}
	}

	_, headErr := client.HeadBucket(opCtx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)})
	if headErr == nil {
		return nil
	}
	return fmt.Errorf("tcp smoke: ensure bucket: %w", err)
}

func putObjectTCP(ctx context.Context, client *s3.Client, cfg Config) error {
	opCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()

	_, err := client.PutObject(opCtx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(cfg.Key),
		Body:   bytes.NewReader(makePayload(cfg.PayloadSize)),
	})
	if err != nil {
		return fmt.Errorf("tcp smoke: put object: %w", err)
	}
	return nil
}

func getObjectTCP(ctx context.Context, client *s3.Client, cfg Config) ([]byte, error) {
	opCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()

	out, err := client.GetObject(opCtx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(cfg.Key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("tcp smoke: read object body: %w", err)
	}
	return body, nil
}

func ensureBucketRDMA(ctx context.Context, client *s3rdmaclient.Client, cfg Config) error {
	opCtx, cancel := context.WithTimeout(ctx, cfg.RDMAControlTO)
	defer cancel()
	if err := client.EnsureBucket(opCtx, cfg.Bucket); err != nil {
		return fmt.Errorf("rdma smoke: ensure bucket: %w", err)
	}
	return nil
}

func putObjectRDMA(ctx context.Context, client *s3rdmaclient.Client, cfg Config) error {
	opCtx, cancel := context.WithTimeout(ctx, cfg.RDMADataTO)
	defer cancel()
	if err := client.PutZeroCopy(opCtx, cfg.Bucket, cfg.Key, s3rdmaclient.BufferRef{
		Offset: cfg.RDMAPutOffset,
		Size:   cfg.PayloadSize,
	}); err != nil {
		return fmt.Errorf("rdma smoke: put object: %w", err)
	}
	return nil
}

func getObjectRDMA(ctx context.Context, client *s3rdmaclient.Client, cfg Config) (s3rdmaclient.BufferRef, error) {
	opCtx, cancel := context.WithTimeout(ctx, cfg.RDMADataTO)
	defer cancel()
	ref, err := client.GetZeroCopy(opCtx, cfg.Bucket, cfg.Key, cfg.RDMAGetOffset, cfg.RDMAGetMaxSize)
	if err != nil {
		return s3rdmaclient.BufferRef{}, err
	}
	return ref, nil
}

func makePayload(size int) []byte {
	body := make([]byte, size)
	fillPayload(body)
	return body
}

func fillPayload(dst []byte) {
	for i := range dst {
		dst[i] = payloadByte(i)
	}
}

func payloadByte(index int) byte {
	return byte((index*31 + 17) & 0xff)
}

func normalizeTCPEndpoint(in string) string {
	v := strings.TrimSpace(in)
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	return "http://" + v
}

func isExpectedTCPSmokeMissing(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nosuchkey") || strings.Contains(msg, "not found")
}

func isExpectedRDMASmokeMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "key not found") || strings.Contains(msg, "no such key")
}

func newSharedMmap(size int) ([]byte, func() error, error) {
	b, err := syscall.Mmap(
		-1,
		0,
		size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED|syscall.MAP_ANONYMOUS|syscall.MAP_POPULATE,
	)
	if err != nil {
		return nil, nil, err
	}

	return b, func() error {
		return syscall.Munmap(b)
	}, nil
}
