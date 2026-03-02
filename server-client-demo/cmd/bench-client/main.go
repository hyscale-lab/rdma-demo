package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
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
	targetRPS        float64
	duration         time.Duration
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
	rdmaMaxConns     int
	rdmaSharePool    bool
	rdmaSharePoolKey string
	rdmaEndpointPool int
	rdmaEndpointWarm bool
	rdmaSharedMemBgt int
	rdmaEndpointMux  bool
	rdmaEndpointSend int
	s3ClientCount    int
	openLoopFanout   bool
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
	BenchmarkModel       string        `json:"benchmark_model"`
	OpenLoopDispatch     string        `json:"open_loop_dispatch,omitempty"`
	Iterations           int           `json:"iterations"`
	Concurrency          int           `json:"concurrency"`
	ClientCount          int           `json:"client_count"`
	TargetRPS            float64       `json:"target_rps,omitempty"`
	TargetDuration       time.Duration `json:"target_duration,omitempty"`
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
	clients, err := newS3Clients(cfg, &stats)
	if err != nil {
		fatalf("create s3 clients: %v", err)
	}
	client := clients[0]

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
			if isOpenLoopMode(cfg) {
				prefillCount = int(math.Ceil(cfg.targetRPS * cfg.duration.Seconds()))
			} else {
				prefillCount = cfg.iterations
			}
		}
		if err := prefill(client, cfg, payload, prefillCount); err != nil {
			fatalf("prefill: %v", err)
		}
	}

	if cfg.warmup > 0 {
		if cfg.verbose {
			fmt.Printf("running warmup: %d ops\n", cfg.warmup)
		}
		if err := runWarmup(clients, cfg, payload, prefillCount); err != nil {
			fatalf("warmup failed: %v", err)
		}
	}

	summary := runBenchmark(clients, cfg, payload, prefillCount, &stats)
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
	flag.StringVar(&cfg.endpoint, "endpoint", "http://127.0.0.1:10090", "S3 endpoint URL (or host:port)")
	flag.StringVar(&cfg.region, "region", "us-east-1", "S3 region")
	flag.StringVar(&cfg.bucket, "bucket", "bench-bucket", "bucket name")
	flag.StringVar(&cfg.keyPrefix, "key-prefix", "bench-object", "object key prefix")
	flag.IntVar(&cfg.iterations, "iterations", 1000, "number of benchmark iterations (closed-loop mode)")
	flag.IntVar(&cfg.concurrency, "concurrency", 16, "number of concurrent workers (closed-loop mode)")
	flag.Float64Var(&cfg.targetRPS, "target-rps", 0, "target requests per second for open-loop mode (>0 enables)")
	flag.DurationVar(&cfg.duration, "duration", 0, "benchmark duration for open-loop mode (for example: 30s)")
	flag.IntVar(&cfg.warmup, "warmup", 100, "warmup operations before measuring")
	flag.IntVar(&cfg.objectSize, "object-size", 4096, "object size in bytes")
	flag.DurationVar(&cfg.requestTimeout, "request-timeout", 5*time.Second, "per-request timeout")
	flag.BoolVar(&cfg.allowFallback, "allow-fallback", false, "allow RDMA dialer to fallback to TCP")
	flag.StringVar(&cfg.accessKey, "access-key", "test", "access key id")
	flag.StringVar(&cfg.secretKey, "secret-key", "test", "secret key")
	flag.BoolVar(&cfg.verbose, "verbose", false, "enable verbose logs")
	flag.StringVar(&cfg.jsonPath, "json", "", "write summary json to path")
	flag.IntVar(&cfg.prefillCount, "prefill", 0, "prefill count for get mode (default=iterations or ceil(target-rps*duration) in open-loop mode)")
	flag.IntVar(&cfg.s3ClientCount, "s3-client-count", 1, "number of S3 client instances to create in-process")
	flag.BoolVar(&cfg.openLoopFanout, "open-loop-client-fanout", false, "in open-loop mode, dispatch requests from per-client schedulers instead of a single global scheduler")

	flag.BoolVar(&cfg.rdmaLowCPU, "rdma-lowcpu", false, "use RDMA low-cpu mode")
	flag.IntVar(&cfg.rdmaFramePayload, "rdma-frame-payload", 0, "RDMA frame payload size (0=default)")
	flag.IntVar(&cfg.rdmaSendDepth, "rdma-send-depth", 0, "RDMA send queue depth (0=default)")
	flag.IntVar(&cfg.rdmaRecvDepth, "rdma-recv-depth", 0, "RDMA recv queue depth (0=default)")
	flag.IntVar(&cfg.rdmaInline, "rdma-inline-threshold", 0, "RDMA inline threshold (0=default)")
	flag.IntVar(&cfg.rdmaSignalIntvl, "rdma-send-signal-interval", 0, "RDMA send signal interval (0=default)")
	flag.IntVar(&cfg.rdmaOpenParallel, "rdma-open-parallelism", awsrdmahttp.DefaultOpenParallelism, "RDMA open parallelism")
	flag.DurationVar(&cfg.rdmaOpenMinIntvl, "rdma-open-min-interval", awsrdmahttp.DefaultOpenMinInterval, "RDMA minimum interval between open attempts")
	flag.IntVar(&cfg.rdmaMaxConns, "rdma-max-conns-per-host", 0, "RDMA max connections per host (<=0 uses adaptive default)")
	flag.BoolVar(&cfg.rdmaSharePool, "rdma-shared-http-pool", false, "enable shared HTTP connection pool")
	flag.StringVar(&cfg.rdmaSharePoolKey, "rdma-shared-http-pool-key", "", "shared HTTP connection pool key (empty uses SDK default key)")
	flag.IntVar(&cfg.rdmaEndpointPool, "rdma-endpoint-pool-size", 0, "RDMA endpoint engine physical connection pool size (<=0 disables)")
	flag.BoolVar(&cfg.rdmaEndpointWarm, "rdma-endpoint-pool-warmup", false, "warm up RDMA endpoint connection pool in background")
	flag.IntVar(&cfg.rdmaSharedMemBgt, "rdma-shared-memory-budget-bytes", 0, "RDMA shared memory budget bytes for endpoint pool accounting (<=0 disables)")
	flag.BoolVar(&cfg.rdmaEndpointMux, "rdma-endpoint-multiplex", false, "enable logical stream multiplexing over RDMA endpoint pool")
	flag.IntVar(&cfg.rdmaEndpointSend, "rdma-endpoint-send-queue-depth", 0, "RDMA multiplexed endpoint send queue depth per physical connection (0=default)")
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
	if cfg.targetRPS < 0 {
		return fmt.Errorf("target-rps must be >= 0")
	}
	if cfg.duration < 0 {
		return fmt.Errorf("duration must be >= 0")
	}
	switch {
	case cfg.targetRPS == 0 && cfg.duration == 0:
		if cfg.iterations <= 0 {
			return fmt.Errorf("iterations must be > 0")
		}
		if cfg.concurrency <= 0 {
			return fmt.Errorf("concurrency must be > 0")
		}
	case cfg.targetRPS > 0 && cfg.duration > 0:
	default:
		return fmt.Errorf("target-rps and duration must be set together")
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
	if cfg.s3ClientCount <= 0 {
		return fmt.Errorf("s3-client-count must be > 0")
	}
	return nil
}

func isOpenLoopMode(cfg benchConfig) bool {
	return cfg.targetRPS > 0 && cfg.duration > 0
}

func normalizeEndpoint(in string) string {
	v := strings.TrimSpace(in)
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	return "http://" + v
}

func newHTTPClient(maxConnsPerHost int) *awshttp.BuildableClient {
	return awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.Proxy = nil
		if maxConnsPerHost > 0 {
			tr.MaxConnsPerHost = maxConnsPerHost
			tr.MaxIdleConnsPerHost = maxConnsPerHost
			if tr.MaxIdleConns < maxConnsPerHost {
				tr.MaxIdleConns = maxConnsPerHost
			}
		}
	})
}

func newS3Clients(cfg benchConfig, stats *dialStats) ([]*s3.Client, error) {
	clients := make([]*s3.Client, 0, cfg.s3ClientCount)

	// In RDMA shared-pool mode, use one concrete S3 client and fan logical
	// client slots to it. This enforces a single underlying transport pool.
	if cfg.mode == "rdma" && cfg.rdmaSharePool && cfg.s3ClientCount > 1 {
		sharedHTTPClient := newHTTPClient(cfg.rdmaMaxConns)
		sharedClient, err := newS3Client(cfg, stats, sharedHTTPClient)
		if err != nil {
			return nil, err
		}
		for i := 0; i < cfg.s3ClientCount; i++ {
			clients = append(clients, sharedClient)
		}
		return clients, nil
	}

	var sharedHTTPClient *awshttp.BuildableClient
	if cfg.mode == "rdma" && cfg.rdmaSharePool {
		sharedHTTPClient = newHTTPClient(cfg.rdmaMaxConns)
	}

	for i := 0; i < cfg.s3ClientCount; i++ {
		httpClient := sharedHTTPClient
		if httpClient == nil {
			maxConns := 0
			if cfg.mode == "rdma" {
				maxConns = cfg.rdmaMaxConns
			}
			httpClient = newHTTPClient(maxConns)
		}

		client, err := newS3Client(cfg, stats, httpClient)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, nil
}

func newS3Client(cfg benchConfig, stats *dialStats, httpClient aws.HTTPClient) (*s3.Client, error) {
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

func selectClient(clients []*s3.Client, idx int) *s3.Client {
	return clients[idx%len(clients)]
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

func runWarmup(clients []*s3.Client, cfg benchConfig, payload []byte, prefillCount int) error {
	for i := 0; i < cfg.warmup; i++ {
		keyIdx := i
		if cfg.op == "get" && prefillCount > 0 {
			keyIdx = i % prefillCount
		}
		key := objectKey(cfg.keyPrefix, keyIdx)
		client := selectClient(clients, i)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
		err := runOperation(ctx, client, cfg, payload, key)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func runBenchmark(clients []*s3.Client, cfg benchConfig, payload []byte, prefillCount int, stats *dialStats) benchSummary {
	if isOpenLoopMode(cfg) {
		return runBenchmarkOpenLoop(clients, cfg, payload, prefillCount, stats)
	}
	return runBenchmarkClosedLoop(clients, cfg, payload, prefillCount, stats)
}

func runBenchmarkClosedLoop(clients []*s3.Client, cfg benchConfig, payload []byte, prefillCount int, stats *dialStats) benchSummary {
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
				client := selectClient(clients, i)
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

	summary := summarize(cfg, durations, failed, elapsed, cfg.iterations)
	summary.FallbackDialAttempts = stats.fallbackDials.Load()
	summary.Errors = errs
	return summary
}

func runBenchmarkOpenLoop(clients []*s3.Client, cfg benchConfig, payload []byte, prefillCount int, stats *dialStats) benchSummary {
	if cfg.openLoopFanout && len(clients) > 1 && cfg.targetRPS >= float64(len(clients)) {
		return runBenchmarkOpenLoopPerClient(clients, cfg, payload, prefillCount, stats)
	}
	return runBenchmarkOpenLoopGlobal(clients, cfg, payload, prefillCount, stats)
}

func openLoopInterval(targetRPS float64) time.Duration {
	interval := time.Duration(float64(time.Second) / targetRPS)
	if interval < time.Nanosecond {
		return time.Nanosecond
	}
	return interval
}

func runBenchmarkOpenLoopGlobal(clients []*s3.Client, cfg benchConfig, payload []byte, prefillCount int, stats *dialStats) benchSummary {
	interval := openLoopInterval(cfg.targetRPS)

	results := make(chan opResult, 4096)
	durations := make([]time.Duration, 0, 1024)
	errs := make([]string, 0, 8)
	failed := 0

	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for r := range results {
			if r.err != nil {
				failed++
				if len(errs) < cap(errs) {
					errs = append(errs, r.err.Error())
				}
				continue
			}
			durations = append(durations, r.latency)
		}
	}()

	var runWG sync.WaitGroup
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timer := time.NewTimer(cfg.duration)
	defer timer.Stop()

	start := time.Now()
	launched := 0
	scheduling := true
	for scheduling {
		select {
		case <-timer.C:
			scheduling = false
		case <-ticker.C:
			i := launched
			launched++
			runWG.Add(1)
			go func(jobIndex int) {
				defer runWG.Done()
				keyIdx := jobIndex
				if cfg.op == "get" && prefillCount > 0 {
					keyIdx = jobIndex % prefillCount
				}
				key := objectKey(cfg.keyPrefix, keyIdx)
				client := selectClient(clients, jobIndex)
				opStart := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
				err := runOperation(ctx, client, cfg, payload, key)
				cancel()
				results <- opResult{
					latency: time.Since(opStart),
					err:     err,
				}
			}(i)
		}
	}

	runWG.Wait()
	close(results)
	collectWG.Wait()

	elapsed := time.Since(start)
	summary := summarize(cfg, durations, failed, elapsed, launched)
	summary.FallbackDialAttempts = stats.fallbackDials.Load()
	summary.Errors = errs
	return summary
}

func runBenchmarkOpenLoopPerClient(clients []*s3.Client, cfg benchConfig, payload []byte, prefillCount int, stats *dialStats) benchSummary {
	perClientRPS := cfg.targetRPS / float64(len(clients))
	interval := openLoopInterval(perClientRPS)

	results := make(chan opResult, 4096)
	durations := make([]time.Duration, 0, 1024)
	errs := make([]string, 0, 8)
	failed := 0

	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for r := range results {
			if r.err != nil {
				failed++
				if len(errs) < cap(errs) {
					errs = append(errs, r.err.Error())
				}
				continue
			}
			durations = append(durations, r.latency)
		}
	}()

	var schedWG sync.WaitGroup
	var runWG sync.WaitGroup
	var launched atomic.Int64
	var nextJobID atomic.Int64
	stopCtx, stop := context.WithCancel(context.Background())

	start := time.Now()
	for idx := range clients {
		client := clients[idx]
		schedWG.Add(1)
		go func(client *s3.Client) {
			defer schedWG.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-stopCtx.Done():
					return
				case <-ticker.C:
					jobID := int(nextJobID.Add(1) - 1)
					launched.Add(1)
					runWG.Add(1)
					go func(jobIndex int, client *s3.Client) {
						defer runWG.Done()
						keyIdx := jobIndex
						if cfg.op == "get" && prefillCount > 0 {
							keyIdx = jobIndex % prefillCount
						}
						key := objectKey(cfg.keyPrefix, keyIdx)
						opStart := time.Now()
						ctx, cancel := context.WithTimeout(context.Background(), cfg.requestTimeout)
						err := runOperation(ctx, client, cfg, payload, key)
						cancel()
						results <- opResult{
							latency: time.Since(opStart),
							err:     err,
						}
					}(jobID, client)
				}
			}
		}(client)
	}

	timer := time.NewTimer(cfg.duration)
	<-timer.C
	stop()
	schedWG.Wait()
	runWG.Wait()
	close(results)
	collectWG.Wait()

	elapsed := time.Since(start)
	summary := summarize(cfg, durations, failed, elapsed, int(launched.Load()))
	summary.FallbackDialAttempts = stats.fallbackDials.Load()
	summary.Errors = errs
	return summary
}

func summarize(cfg benchConfig, durations []time.Duration, failed int, elapsed time.Duration, iterations int) benchSummary {
	success := len(durations)
	benchmarkModel := "closed-loop"
	concurrency := cfg.concurrency
	if isOpenLoopMode(cfg) {
		benchmarkModel = "open-loop"
		concurrency = 0
	}

	summary := benchSummary{
		Timestamp:      time.Now().Format(time.RFC3339),
		Mode:           cfg.mode,
		Operation:      cfg.op,
		Endpoint:       cfg.endpoint,
		Region:         cfg.region,
		Bucket:         cfg.bucket,
		BenchmarkModel: benchmarkModel,
		Iterations:     iterations,
		Concurrency:    concurrency,
		ClientCount:    cfg.s3ClientCount,
		TargetRPS:      cfg.targetRPS,
		TargetDuration: cfg.duration,
		Warmup:         cfg.warmup,
		ObjectSize:     cfg.objectSize,
		Elapsed:        elapsed,
		Success:        success,
		Failed:         failed,
	}
	if isOpenLoopMode(cfg) {
		if cfg.openLoopFanout {
			summary.OpenLoopDispatch = "per-client-fanout"
		} else {
			summary.OpenLoopDispatch = "single-global"
		}
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
	fmt.Printf("mode=%s op=%s endpoint=%s model=%s\n", s.Mode, s.Operation, s.Endpoint, s.BenchmarkModel)
	if s.BenchmarkModel == "open-loop" {
		fmt.Printf("launched=%d target_rps=%.2f target_duration=%s warmup=%d object_size=%d client_count=%d dispatch=%s\n",
			s.Iterations, s.TargetRPS, s.TargetDuration, s.Warmup, s.ObjectSize, s.ClientCount, s.OpenLoopDispatch)
	} else {
		fmt.Printf("iterations=%d concurrency=%d warmup=%d object_size=%d client_count=%d\n",
			s.Iterations, s.Concurrency, s.Warmup, s.ObjectSize, s.ClientCount)
	}
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
