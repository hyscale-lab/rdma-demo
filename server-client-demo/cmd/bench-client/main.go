package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsrdmahttp "github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type benchConfig struct {
	mode             string
	op               string
	endpoint         string
	region           string
	bucket           string
	keyPrefix        string
	iterations       int
	concurrency      int
	warmup           int
	objectSize       int
	requestTimeout   time.Duration
	allowFallback    bool
	accessKey        string
	secretKey        string
	verbose          bool
	jsonPath         string
	prefillCount     int
	rdmaLowCPU       bool
	rdmaFramePayload int
	rdmaSendDepth    int
	rdmaRecvDepth    int
	rdmaInline       int
	rdmaSignalIntvl  int
	rdmaOpenParallel int
	rdmaOpenMinIntvl time.Duration
}

type dialStats struct {
	fallbackDials atomic.Int64
}

type opResult struct {
	latency time.Duration
	err     error
}

type benchSummary struct {
	Timestamp            string        `json:"timestamp"`
	Mode                 string        `json:"mode"`
	Operation            string        `json:"operation"`
	Endpoint             string        `json:"endpoint"`
	Region               string        `json:"region"`
	Bucket               string        `json:"bucket"`
	Iterations           int           `json:"iterations"`
	Concurrency          int           `json:"concurrency"`
	Warmup               int           `json:"warmup"`
	ObjectSize           int           `json:"object_size"`
	Elapsed              time.Duration `json:"elapsed"`
	Success              int           `json:"success"`
	Failed               int           `json:"failed"`
	ThroughputReqPerSec  float64       `json:"throughput_req_per_sec"`
	ThroughputMBPerSec   float64       `json:"throughput_mb_per_sec"`
	AverageLatency       time.Duration `json:"avg_latency"`
	P50Latency           time.Duration `json:"p50_latency"`
	P95Latency           time.Duration `json:"p95_latency"`
	P99Latency           time.Duration `json:"p99_latency"`
	MinLatency           time.Duration `json:"min_latency"`
	MaxLatency           time.Duration `json:"max_latency"`
	FallbackDialAttempts int64         `json:"fallback_dial_attempts"`
	Errors               []string      `json:"errors,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := validateConfig(cfg); err != nil {
		fatalf("invalid config: %v", err)
	}

	var stats dialStats
	client, err := newS3Client(cfg, &stats)
	if err != nil {
		fatalf("create s3 client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := ensureBucket(ctx, client, cfg.bucket); err != nil {
		cancel()
		fatalf("ensure bucket: %v", err)
	}
	cancel()

	payload := makePayload(cfg.objectSize)
	prefillCount := cfg.prefillCount
	if cfg.op == "get" {
		if prefillCount <= 0 {
			prefillCount = cfg.iterations
		}
		if err := prefill(client, cfg, payload, prefillCount); err != nil {
			fatalf("prefill: %v", err)
		}
	}

	if cfg.warmup > 0 {
		if cfg.verbose {
			fmt.Printf("running warmup: %d ops\n", cfg.warmup)
		}
		if err := runWarmup(client, cfg, payload, prefillCount); err != nil {
			fatalf("warmup failed: %v", err)
		}
	}

	summary := runBenchmark(client, cfg, payload, prefillCount, &stats)
	printSummary(summary)
	if cfg.jsonPath != "" {
		if err := writeSummaryJSON(cfg.jsonPath, summary); err != nil {
			fatalf("write summary json: %v", err)
		}
	}
}

func parseFlags() benchConfig {
	cfg := benchConfig{}
	flag.StringVar(&cfg.mode, "mode", "tcp", "transport mode: tcp or rdma")
	flag.StringVar(&cfg.op, "op", "put-get", "benchmark operation: put, get, put-get")
	flag.StringVar(&cfg.endpoint, "endpoint", "http://127.0.0.1:9000", "S3 endpoint URL (or host:port)")
	flag.StringVar(&cfg.region, "region", "us-east-1", "S3 region")
	flag.StringVar(&cfg.bucket, "bucket", "bench-bucket", "bucket name")
	flag.StringVar(&cfg.keyPrefix, "key-prefix", "bench-object", "object key prefix")
	flag.IntVar(&cfg.iterations, "iterations", 1000, "number of benchmark iterations")
	flag.IntVar(&cfg.concurrency, "concurrency", 16, "number of concurrent workers")
	flag.IntVar(&cfg.warmup, "warmup", 100, "warmup operations before measuring")
	flag.IntVar(&cfg.objectSize, "object-size", 4096, "object size in bytes")
	flag.DurationVar(&cfg.requestTimeout, "request-timeout", 5*time.Second, "per-request timeout")
	flag.BoolVar(&cfg.allowFallback, "allow-fallback", false, "allow RDMA dialer to fallback to TCP")
	flag.StringVar(&cfg.accessKey, "access-key", "test", "access key id")
	flag.StringVar(&cfg.secretKey, "secret-key", "test", "secret key")
	flag.BoolVar(&cfg.verbose, "verbose", false, "enable verbose logs")
	flag.StringVar(&cfg.jsonPath, "json", "", "write summary json to path")
	flag.IntVar(&cfg.prefillCount, "prefill", 0, "prefill count for get mode (default=iterations)")

	flag.BoolVar(&cfg.rdmaLowCPU, "rdma-lowcpu", false, "use RDMA low-cpu mode")
	flag.IntVar(&cfg.rdmaFramePayload, "rdma-frame-payload", 0, "RDMA frame payload size (0=default)")
	flag.IntVar(&cfg.rdmaSendDepth, "rdma-send-depth", 0, "RDMA send queue depth (0=default)")
	flag.IntVar(&cfg.rdmaRecvDepth, "rdma-recv-depth", 0, "RDMA recv queue depth (0=default)")
	flag.IntVar(&cfg.rdmaInline, "rdma-inline-threshold", 0, "RDMA inline threshold (0=default)")
	flag.IntVar(&cfg.rdmaSignalIntvl, "rdma-send-signal-interval", 0, "RDMA send signal interval (0=default)")
	flag.IntVar(&cfg.rdmaOpenParallel, "rdma-open-parallelism", awsrdmahttp.DefaultOpenParallelism, "RDMA open parallelism")
	flag.DurationVar(&cfg.rdmaOpenMinIntvl, "rdma-open-min-interval", awsrdmahttp.DefaultOpenMinInterval, "RDMA minimum interval between open attempts")
	flag.Parse()

	cfg.mode = strings.ToLower(strings.TrimSpace(cfg.mode))
	cfg.op = strings.ToLower(strings.TrimSpace(cfg.op))
	cfg.endpoint = normalizeEndpoint(cfg.endpoint)
	return cfg
}

func validateConfig(cfg benchConfig) error {
	if cfg.mode != "tcp" && cfg.mode != "rdma" {
		return fmt.Errorf("mode must be tcp or rdma")
	}
	switch cfg.op {
	case "put", "get", "put-get":
	default:
		return fmt.Errorf("op must be put, get, or put-get")
	}
	if cfg.iterations <= 0 {
		return fmt.Errorf("iterations must be > 0")
	}
	if cfg.concurrency <= 0 {
		return fmt.Errorf("concurrency must be > 0")
	}
	if cfg.objectSize < 0 {
		return fmt.Errorf("object-size must be >= 0")
	}
	if cfg.requestTimeout <= 0 {
		return fmt.Errorf("request-timeout must be > 0")
	}
	if cfg.bucket == "" {
		return fmt.Errorf("bucket must not be empty")
	}
	if cfg.keyPrefix == "" {
		return fmt.Errorf("key-prefix must not be empty")
	}
	return nil
}

func normalizeEndpoint(in string) string {
	v := strings.TrimSpace(in)
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	return "http://" + v
}

func newS3Client(cfg benchConfig, stats *dialStats) (*s3.Client, error) {
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.Proxy = nil
	})

	opts := s3.Options{
		Region:       cfg.region,
		BaseEndpoint: aws.String(cfg.endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.accessKey, cfg.secretKey, ""),
		HTTPClient:   httpClient,
	}

	if cfg.mode == "rdma" {
		dialer := awsrdmahttp.NewVerbsDialer(awsrdmahttp.VerbsOptions{
			FramePayloadSize:   cfg.rdmaFramePayload,
			SendQueueDepth:     cfg.rdmaSendDepth,
			RecvQueueDepth:     cfg.rdmaRecvDepth,
			InlineThreshold:    cfg.rdmaInline,
			LowCPU:             cfg.rdmaLowCPU,
			SendSignalInterval: cfg.rdmaSignalIntvl,
		})
		dialer.DisableFallback = !cfg.allowFallback
		dialer.OpenParallelism = cfg.rdmaOpenParallel
		dialer.OpenMinInterval = cfg.rdmaOpenMinIntvl
		dialer.FallbackDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			stats.fallbackDials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		opts.EnableRDMATransport = true
		opts.RDMADialer = dialer
	}

	return s3.New(opts), nil
}

func ensureBucket(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if code == "BucketAlreadyOwnedByYou" || code == "BucketAlreadyExists" {
			return nil
		}
	}

	_, headErr := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if headErr == nil {
		return nil
	}
	return fmt.Errorf("create bucket: %w", err)
}

func prefill(client *s3.Client, cfg benchConfig, payload []byte, count int) error {
	if cfg.verbose {
		fmt.Printf("prefill %d objects\n", count)
	}
	for i := 0; i < count; i++ {
		key := objectKey(cfg.keyPrefix, i)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
		err := putObject(ctx, client, cfg.bucket, key, payload)
		cancel()
		if err != nil {
			return fmt.Errorf("prefill key=%s: %w", key, err)
		}
	}
	return nil
}

func runWarmup(client *s3.Client, cfg benchConfig, payload []byte, prefillCount int) error {
	for i := 0; i < cfg.warmup; i++ {
		keyIdx := i
		if cfg.op == "get" && prefillCount > 0 {
			keyIdx = i % prefillCount
		}
		key := objectKey(cfg.keyPrefix, keyIdx)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
		err := runOperation(ctx, client, cfg, payload, key)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func runBenchmark(client *s3.Client, cfg benchConfig, payload []byte, prefillCount int, stats *dialStats) benchSummary {
	jobs := make(chan int)
	results := make(chan opResult, cfg.iterations)

	var wg sync.WaitGroup
	for w := 0; w < cfg.concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				keyIdx := i
				if cfg.op == "get" && prefillCount > 0 {
					keyIdx = i % prefillCount
				}
				key := objectKey(cfg.keyPrefix, keyIdx)
				start := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
				err := runOperation(ctx, client, cfg, payload, key)
				cancel()
				results <- opResult{
					latency: time.Since(start),
					err:     err,
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := 0; i < cfg.iterations; i++ {
			jobs <- i
		}
	}()

	start := time.Now()
	durations := make([]time.Duration, 0, cfg.iterations)
	errs := make([]string, 0, 8)
	failed := 0
	for i := 0; i < cfg.iterations; i++ {
		r := <-results
		if r.err != nil {
			failed++
			if len(errs) < cap(errs) {
				errs = append(errs, r.err.Error())
			}
			continue
		}
		durations = append(durations, r.latency)
	}
	elapsed := time.Since(start)

	wg.Wait()

	summary := summarize(cfg, durations, failed, elapsed)
	summary.FallbackDialAttempts = stats.fallbackDials.Load()
	summary.Errors = errs
	return summary
}

func summarize(cfg benchConfig, durations []time.Duration, failed int, elapsed time.Duration) benchSummary {
	success := len(durations)
	summary := benchSummary{
		Timestamp:   time.Now().Format(time.RFC3339),
		Mode:        cfg.mode,
		Operation:   cfg.op,
		Endpoint:    cfg.endpoint,
		Region:      cfg.region,
		Bucket:      cfg.bucket,
		Iterations:  cfg.iterations,
		Concurrency: cfg.concurrency,
		Warmup:      cfg.warmup,
		ObjectSize:  cfg.objectSize,
		Elapsed:     elapsed,
		Success:     success,
		Failed:      failed,
	}
	if success == 0 {
		return summary
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	var total time.Duration
	for _, d := range durations {
		total += d
	}
	summary.AverageLatency = total / time.Duration(success)
	summary.MinLatency = durations[0]
	summary.MaxLatency = durations[len(durations)-1]
	summary.P50Latency = percentile(durations, 0.50)
	summary.P95Latency = percentile(durations, 0.95)
	summary.P99Latency = percentile(durations, 0.99)
	summary.ThroughputReqPerSec = float64(success) / elapsed.Seconds()

	bytesPerOp := cfg.objectSize
	if cfg.op == "put-get" {
		bytesPerOp = bytesPerOp * 2
	}
	summary.ThroughputMBPerSec = float64(success*bytesPerOp) / elapsed.Seconds() / (1024 * 1024)
	return summary
}

func percentile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	if p <= 0 {
		return durations[0]
	}
	if p >= 1 {
		return durations[len(durations)-1]
	}
	idx := int(float64(len(durations)-1) * p)
	return durations[idx]
}

func runOperation(ctx context.Context, client *s3.Client, cfg benchConfig, payload []byte, key string) error {
	switch cfg.op {
	case "put":
		return putObject(ctx, client, cfg.bucket, key, payload)
	case "get":
		return getObject(ctx, client, cfg.bucket, key)
	case "put-get":
		if err := putObject(ctx, client, cfg.bucket, key, payload); err != nil {
			return err
		}
		return getObject(ctx, client, cfg.bucket, key)
	default:
		return fmt.Errorf("unsupported op %q", cfg.op)
	}
}

func putObject(ctx context.Context, client *s3.Client, bucket, key string, payload []byte) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(payload),
		ContentLength: aws.Int64(int64(len(payload))),
	})
	return err
}

func getObject(ctx context.Context, client *s3.Client, bucket, key string) error {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return err
	}
	defer out.Body.Close()
	_, err = io.Copy(io.Discard, out.Body)
	return err
}

func objectKey(prefix string, i int) string {
	return fmt.Sprintf("%s-%08d", prefix, i)
}

func makePayload(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*31 + 7) & 0xff)
	}
	return out
}

func printSummary(s benchSummary) {
	fmt.Printf("mode=%s op=%s endpoint=%s\n", s.Mode, s.Operation, s.Endpoint)
	fmt.Printf("iterations=%d concurrency=%d warmup=%d object_size=%d\n", s.Iterations, s.Concurrency, s.Warmup, s.ObjectSize)
	fmt.Printf("success=%d failed=%d elapsed=%s\n", s.Success, s.Failed, s.Elapsed)
	fmt.Printf("throughput: %.2f req/s, %.2f MiB/s\n", s.ThroughputReqPerSec, s.ThroughputMBPerSec)
	fmt.Printf("latency: avg=%s p50=%s p95=%s p99=%s min=%s max=%s\n",
		s.AverageLatency, s.P50Latency, s.P95Latency, s.P99Latency, s.MinLatency, s.MaxLatency)
	fmt.Printf("fallback_dial_attempts=%d\n", s.FallbackDialAttempts)
	if len(s.Errors) > 0 {
		fmt.Println("sample_errors:")
		for _, err := range s.Errors {
			fmt.Printf("  - %s\n", err)
		}
	}
}

func writeSummaryJSON(path string, summary benchSummary) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
