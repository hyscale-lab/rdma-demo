package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	awsrdmahttp "github.com/hyscale-lab/rdma-demo/rdma"

	"github.com/hyscale-lab/rdma-demo/rdma/client"
)

type demoClient struct {
	id      int
	key     string
	shared  []byte
	client  *s3rdmaclient.Client
	cleanup func()
}

type runtimeSample struct {
	maxThreads    int
	maxGoroutines int
}

type benchConfig struct {
	op           string
	bucket       string
	putOffset    int
	putSize      int
	getOffset    int
	maxGetSize   int
	concurrent   int
	targetRPS    float64
	duration     time.Duration
	reqTimeout   time.Duration
	verifyResult bool
}

type opResult struct {
	clientID int
	ref      s3rdmaclient.BufferRef
	latency  time.Duration
	err      error
}

type benchStats struct {
	mode         string
	launched     int
	success      int
	failed       int
	elapsed      time.Duration
	throughput   float64
	avg          time.Duration
	p50          time.Duration
	p95          time.Duration
	p99          time.Duration
	min          time.Duration
	max          time.Duration
	errors       []string
	lastClientID int
	lastRef      s3rdmaclient.BufferRef
}

func main() {
	var (
		endpoint      string
		bucket        string
		key           string
		op            string
		memSize       int
		putOffset     int
		putSize       int
		getOffset     int
		maxGetSize    int
		concurrent    int
		clientCount   int
		statsInterval time.Duration
		targetRPS     float64
		duration      time.Duration
		verifyResult  bool
		reqTimeout    time.Duration
		frameSize     int
		sendDepth     int
		recvDepth     int
		inlineBytes   int
		maxCredits    int
		sendQueueLen  int
	)

	flag.StringVar(&endpoint, "endpoint", "127.0.0.1:10191", "zcopy RDMA endpoint")
	flag.StringVar(&bucket, "bucket", "bench-bucket", "bucket name")
	flag.StringVar(&key, "key", "zcopy-demo", "object key")
	flag.StringVar(&op, "op", "get", "operation mode: get or put-get")
	flag.IntVar(&memSize, "mem-size", 32*1024*1024, "shared mmap region size in bytes")
	flag.IntVar(&putOffset, "put-offset", 0, "PUT payload offset in shared memory")
	flag.IntVar(&putSize, "put-size", 256*1024, "PUT payload size in bytes")
	flag.IntVar(&getOffset, "get-offset", 1024*1024, "GET target offset in shared memory")
	flag.IntVar(&maxGetSize, "get-max-size", 512*1024, "GET max writable size in shared memory")
	flag.IntVar(&concurrent, "concurrent-get", 1, "number of concurrent GET operations per client (closed-burst mode only)")
	flag.IntVar(&clientCount, "client-count", 1, "number of s3rdmaclient instances in one process")
	flag.DurationVar(&statsInterval, "stats-interval", time.Second, "runtime stats sampling interval (<=0 disables)")
	flag.Float64Var(&targetRPS, "target-rps", 0, "global target requests per second (>0 enables open-loop mode)")
	flag.DurationVar(&duration, "duration", 0, "open-loop benchmark duration (for example: 20s)")
	flag.BoolVar(&verifyResult, "verify-result", true, "verify GET payload bytes")
	flag.DurationVar(&reqTimeout, "request-timeout", 10*time.Second, "per request timeout")
	flag.IntVar(&frameSize, "rdma-frame-payload", 0, "RDMA frame payload size (0=default)")
	flag.IntVar(&sendDepth, "rdma-send-depth", 0, "RDMA send queue depth (0=default)")
	flag.IntVar(&recvDepth, "rdma-recv-depth", 0, "RDMA recv queue depth (0=default)")
	flag.IntVar(&inlineBytes, "rdma-inline-threshold", 0, "RDMA inline threshold (0=default)")
	flag.IntVar(&maxCredits, "max-credits", 0, "max in-flight GET credits advertised in hello (0=client default)")
	flag.IntVar(&sendQueueLen, "send-queue-len", 0, "per-client serialized send queue length (0=client default)")
	flag.Parse()

	op = strings.ToLower(strings.TrimSpace(op))
	switch op {
	case "get", "put-get":
	default:
		fatalf("op must be one of: get, put-get")
	}

	openLoop := false
	switch {
	case targetRPS == 0 && duration == 0:
	case targetRPS > 0 && duration > 0:
		openLoop = true
	default:
		fatalf("target-rps and duration must be set together")
	}

	if memSize <= 0 {
		fatalf("mem-size must be > 0")
	}
	if putOffset < 0 || putSize < 0 || putOffset+putSize > memSize {
		fatalf("invalid put range offset=%d size=%d mem=%d", putOffset, putSize, memSize)
	}
	if getOffset < 0 || maxGetSize <= 0 {
		fatalf("invalid get range offset=%d max=%d", getOffset, maxGetSize)
	}
	if clientCount <= 0 {
		fatalf("client-count must be > 0")
	}
	if reqTimeout <= 0 {
		fatalf("request-timeout must be > 0")
	}
	if maxCredits < 0 {
		fatalf("max-credits must be >= 0")
	}
	if sendQueueLen < 0 {
		fatalf("send-queue-len must be >= 0")
	}
	if openLoop {
		if targetRPS <= 0 {
			fatalf("target-rps must be > 0")
		}
		if duration <= 0 {
			fatalf("duration must be > 0")
		}
		if getOffset+maxGetSize > memSize {
			fatalf("invalid get range offset=%d max=%d mem=%d", getOffset, maxGetSize, memSize)
		}
		if concurrent != 1 {
			fmt.Fprintf(os.Stderr, "note: concurrent-get=%d is ignored in open-loop mode; each launched request is one operation\n", concurrent)
		}
	} else {
		getWindow, ok := mulIntChecked(concurrent, maxGetSize)
		if !ok {
			fatalf("invalid get window: concurrent=%d max=%d overflows int", concurrent, maxGetSize)
		}
		if concurrent <= 0 {
			fatalf("concurrent-get must be > 0")
		}
		if getOffset+getWindow > memSize {
			fatalf("invalid get range offset=%d max=%d concurrent=%d mem=%d", getOffset, maxGetSize, concurrent, memSize)
		}
	}

	startGoroutines := runtime.NumGoroutine()
	startCGOCalls := runtime.NumCgoCall()
	startThreads, startThreadsErr := readThreadCount()
	if startThreadsErr != nil {
		startThreads = -1
	}

	stopStats := make(chan struct{})
	statsDone := make(chan runtimeSample, 1)
	if statsInterval > 0 {
		go monitorRuntime(statsInterval, stopStats, statsDone)
	}

	clients := make([]demoClient, 0, clientCount)
	defer func() {
		var wg sync.WaitGroup
		errCh := make(chan error, len(clients))
		for _, client := range clients {
			client := client
			wg.Add(1)
			go func() {
				defer wg.Done()
				if client.client != nil {
					if err := client.client.Close(); err != nil {
						errCh <- fmt.Errorf("client=%d close failed: %w", client.id, err)
					}
				}
				if client.cleanup != nil {
					client.cleanup()
				}
			}()
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			fmt.Fprintf(os.Stderr, "cleanup warning: %v\n", err)
		}
	}()

	for clientID := 0; clientID < clientCount; clientID++ {
		shared, cleanup, err := newSharedMmap(memSize)
		if err != nil {
			fatalf("mmap shared memory (client=%d): %v", clientID, err)
		}
		for i := 0; i < putSize; i++ {
			shared[putOffset+i] = payloadByte(clientID, i)
		}
		cli, err := s3rdmaclient.New(s3rdmaclient.Config{
			Endpoint:       endpoint,
			RequestTimeout: reqTimeout,
			MaxCredits:     maxCredits,
			SendQueueLen:   sendQueueLen,
			VerbsOptions: awsrdmahttp.VerbsOptions{
				FramePayloadSize: frameSize,
				SendQueueDepth:   sendDepth,
				RecvQueueDepth:   recvDepth,
				InlineThreshold:  inlineBytes,
			},
		}, shared)
		if err != nil {
			cleanup()
			fatalf("init s3rdmaclient (client=%d): %v", clientID, err)
		}
		clients = append(clients, demoClient{
			id:      clientID,
			key:     objectKey(key, clientID, clientCount),
			shared:  shared,
			client:  cli,
			cleanup: cleanup,
		})
	}

	ctx := context.Background()
	if err := clients[0].client.EnsureBucket(ctx, bucket); err != nil {
		fatalf("ensure bucket failed: %v", err)
	}
	if op == "get" {
		for _, c := range clients {
			if err := c.client.PutZeroCopy(ctx, bucket, c.key, s3rdmaclient.BufferRef{Offset: putOffset, Size: putSize}); err != nil {
				fatalf("put zero-copy failed (client=%d): %v", c.id, err)
			}
		}
	}

	runCfg := benchConfig{
		op:           op,
		bucket:       bucket,
		putOffset:    putOffset,
		putSize:      putSize,
		getOffset:    getOffset,
		maxGetSize:   maxGetSize,
		concurrent:   concurrent,
		targetRPS:    targetRPS,
		duration:     duration,
		reqTimeout:   reqTimeout,
		verifyResult: verifyResult,
	}
	var stats benchStats
	if openLoop {
		stats = runOpenLoop(clients, runCfg)
	} else {
		stats = runClosedBurst(clients, runCfg)
	}
	if stats.failed > 0 {
		for _, msg := range stats.errors {
			fmt.Fprintf(os.Stderr, "sample error: %s\n", msg)
		}
		fatalf("benchmark failed success=%d failed=%d", stats.success, stats.failed)
	}

	maxThreads := startThreads
	maxGoroutines := startGoroutines
	if statsInterval > 0 {
		close(stopStats)
		sample := <-statsDone
		if sample.maxThreads > maxThreads {
			maxThreads = sample.maxThreads
		}
		if sample.maxGoroutines > maxGoroutines {
			maxGoroutines = sample.maxGoroutines
		}
	}
	endThreads, endThreadsErr := readThreadCount()
	if endThreadsErr != nil {
		endThreads = -1
	}
	endGoroutines := runtime.NumGoroutine()
	endCGOCalls := runtime.NumCgoCall()
	if endThreads > maxThreads {
		maxThreads = endThreads
	}
	if endGoroutines > maxGoroutines {
		maxGoroutines = endGoroutines
	}

	fmt.Printf(
		"zcopy demo ok endpoint=%s bucket=%s key=%s op=%s mode=%s clients=%d mem_per_client=%d put=[%d,%d)\n",
		endpoint,
		bucket,
		key,
		op,
		stats.mode,
		clientCount,
		memSize,
		putOffset,
		putOffset+putSize,
	)
	printBenchStats(stats)
	fmt.Printf(
		"runtime stats: goroutines start=%d peak=%d end=%d threads start=%d peak=%d end=%d cgo_calls_delta=%d\n",
		startGoroutines,
		maxGoroutines,
		endGoroutines,
		startThreads,
		maxThreads,
		endThreads,
		endCGOCalls-startCGOCalls,
	)
	if startThreadsErr != nil || endThreadsErr != nil {
		fmt.Printf("thread stat warning: start_err=%v end_err=%v\n", startThreadsErr, endThreadsErr)
	}
}

func runOpenLoop(clients []demoClient, cfg benchConfig) benchStats {
	interval := openLoopInterval(cfg.targetRPS)
	results := make(chan opResult, 4096)

	var (
		durations    = make([]time.Duration, 0, 1024)
		errs         = make([]string, 0, 8)
		failed       = 0
		lastClientID = -1
		lastRef      s3rdmaclient.BufferRef
	)
	var collectMu sync.Mutex
	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for res := range results {
			collectMu.Lock()
			if res.err != nil {
				failed++
				if len(errs) < cap(errs) {
					errs = append(errs, res.err.Error())
				}
				collectMu.Unlock()
				continue
			}
			durations = append(durations, res.latency)
			lastClientID = res.clientID
			lastRef = res.ref
			collectMu.Unlock()
		}
	}()

	slotCounts := make([]int, len(clients))
	for i, c := range clients {
		slotCounts[i] = availableSlots(len(c.shared), cfg.getOffset, cfg.maxGetSize)
	}

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
			jobIdx := launched
			launched++
			clientIdx := jobIdx % len(clients)
			c := clients[clientIdx]
			slots := slotCounts[clientIdx]
			slot := 0
			if slots > 1 {
				slot = jobIdx % slots
			}
			targetOffset := cfg.getOffset + slot*cfg.maxGetSize

			runWG.Add(1)
			go func(client demoClient, requestID int, offset int) {
				defer runWG.Done()
				opStart := time.Now()
				ref, err := executeOperation(client, cfg, requestID, offset)
				results <- opResult{
					clientID: client.id,
					ref:      ref,
					latency:  time.Since(opStart),
					err:      err,
				}
			}(c, jobIdx, targetOffset)
		}
	}

	runWG.Wait()
	close(results)
	collectWG.Wait()

	elapsed := time.Since(start)
	return summarizeBench("open-loop", launched, durations, failed, elapsed, errs, lastClientID, lastRef)
}

func runClosedBurst(clients []demoClient, cfg benchConfig) benchStats {
	totalGets, ok := mulIntChecked(len(clients), cfg.concurrent)
	if !ok {
		return benchStats{
			mode:     "closed-burst",
			launched: 0,
			failed:   1,
			errors: []string{
				fmt.Sprintf("invalid total gets: clients=%d concurrent-get=%d", len(clients), cfg.concurrent),
			},
		}
	}

	results := make(chan opResult, totalGets)
	var runWG sync.WaitGroup
	start := time.Now()

	for _, c := range clients {
		for slot := 0; slot < cfg.concurrent; slot++ {
			c := c
			slot := slot
			targetOffset := cfg.getOffset + slot*cfg.maxGetSize
			jobID := c.id*cfg.concurrent + slot
			runWG.Add(1)
			go func(client demoClient, requestID int, offset int) {
				defer runWG.Done()
				opStart := time.Now()
				ref, err := executeOperation(client, cfg, requestID, offset)
				results <- opResult{
					clientID: client.id,
					ref:      ref,
					latency:  time.Since(opStart),
					err:      err,
				}
			}(c, jobID, targetOffset)
		}
	}

	runWG.Wait()
	close(results)

	durations := make([]time.Duration, 0, totalGets)
	errs := make([]string, 0, 8)
	failed := 0
	lastClientID := -1
	var lastRef s3rdmaclient.BufferRef
	for res := range results {
		if res.err != nil {
			failed++
			if len(errs) < cap(errs) {
				errs = append(errs, res.err.Error())
			}
			continue
		}
		durations = append(durations, res.latency)
		lastClientID = res.clientID
		lastRef = res.ref
	}

	elapsed := time.Since(start)
	return summarizeBench("closed-burst", totalGets, durations, failed, elapsed, errs, lastClientID, lastRef)
}

func executeOperation(client demoClient, cfg benchConfig, requestID int, targetOffset int) (ref s3rdmaclient.BufferRef, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.reqTimeout)
	defer cancel()

	if cfg.op == "put-get" {
		err = client.client.PutZeroCopy(ctx, cfg.bucket, client.key, s3rdmaclient.BufferRef{
			Offset: cfg.putOffset,
			Size:   cfg.putSize,
		})
		if err != nil {
			return s3rdmaclient.BufferRef{}, fmt.Errorf("put failed client=%d req=%d: %w", client.id, requestID, err)
		}
	}

	ref, err = client.client.GetZeroCopy(ctx, cfg.bucket, client.key, targetOffset, cfg.maxGetSize)
	if err != nil {
		return s3rdmaclient.BufferRef{}, fmt.Errorf("get failed client=%d req=%d: %w", client.id, requestID, err)
	}
	defer func() {
		if relErr := client.client.Release(ref); relErr != nil && err == nil {
			err = fmt.Errorf("release failed client=%d req=%d: %w", client.id, requestID, relErr)
		}
	}()

	if ref.Size != cfg.putSize {
		return ref, fmt.Errorf("size mismatch client=%d req=%d put=%d get=%d", client.id, requestID, cfg.putSize, ref.Size)
	}
	if !cfg.verifyResult {
		return ref, nil
	}
	for i := 0; i < cfg.putSize; i++ {
		expected := payloadByte(client.id, i)
		got := client.shared[ref.Offset+i]
		if got != expected {
			return ref, fmt.Errorf("data mismatch client=%d req=%d byte=%d expected=%d got=%d", client.id, requestID, i, expected, got)
		}
	}
	return ref, nil
}

func summarizeBench(
	mode string,
	launched int,
	durations []time.Duration,
	failed int,
	elapsed time.Duration,
	errors []string,
	lastClientID int,
	lastRef s3rdmaclient.BufferRef,
) benchStats {
	success := len(durations)
	stats := benchStats{
		mode:         mode,
		launched:     launched,
		success:      success,
		failed:       failed,
		elapsed:      elapsed,
		errors:       errors,
		lastClientID: lastClientID,
		lastRef:      lastRef,
	}
	if elapsed > 0 {
		stats.throughput = float64(success) / elapsed.Seconds()
	}
	if success == 0 {
		return stats
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	var total time.Duration
	for _, d := range durations {
		total += d
	}
	stats.avg = total / time.Duration(success)
	stats.min = durations[0]
	stats.max = durations[success-1]
	stats.p50 = percentile(durations, 0.50)
	stats.p95 = percentile(durations, 0.95)
	stats.p99 = percentile(durations, 0.99)
	return stats
}

func printBenchStats(stats benchStats) {
	fmt.Printf("benchmark mode=%s launched=%d success=%d failed=%d elapsed=%s throughput=%.2f req/s\n",
		stats.mode,
		stats.launched,
		stats.success,
		stats.failed,
		stats.elapsed,
		stats.throughput,
	)
	fmt.Printf("latency avg=%s p50=%s p95=%s p99=%s min=%s max=%s\n",
		stats.avg,
		stats.p50,
		stats.p95,
		stats.p99,
		stats.min,
		stats.max,
	)
	if stats.lastClientID >= 0 && stats.lastRef.Size > 0 {
		fmt.Printf("last_ref client=%d offset=%d size=%d\n", stats.lastClientID, stats.lastRef.Offset, stats.lastRef.Size)
	}
}

func availableSlots(memSize, getOffset, maxGetSize int) int {
	if maxGetSize <= 0 {
		return 1
	}
	space := memSize - getOffset
	if space <= 0 {
		return 1
	}
	slots := space / maxGetSize
	if slots < 1 {
		return 1
	}
	return slots
}

func openLoopInterval(targetRPS float64) time.Duration {
	interval := time.Duration(float64(time.Second) / targetRPS)
	if interval < time.Nanosecond {
		return time.Nanosecond
	}
	return interval
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

func objectKey(base string, clientID, clientCount int) string {
	if clientCount <= 1 {
		return base
	}
	return fmt.Sprintf("%s-c%03d", base, clientID)
}

func payloadByte(clientID, index int) byte {
	return byte((index*31 + 17 + clientID*13) & 0xff)
}

func mulIntChecked(a, b int) (int, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a == 0 || b == 0 {
		return 0, true
	}
	maxInt := int(^uint(0) >> 1)
	if a > maxInt/b {
		return 0, false
	}
	return a * b, true
}

func monitorRuntime(interval time.Duration, stop <-chan struct{}, done chan<- runtimeSample) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	maxThreads, err := readThreadCount()
	if err != nil {
		maxThreads = -1
	}
	maxGoroutines := runtime.NumGoroutine()

	update := func() {
		if g := runtime.NumGoroutine(); g > maxGoroutines {
			maxGoroutines = g
		}
		if t, err := readThreadCount(); err == nil && t > maxThreads {
			maxThreads = t
		}
	}

	for {
		select {
		case <-ticker.C:
			update()
		case <-stop:
			update()
			done <- runtimeSample{
				maxThreads:    maxThreads,
				maxGoroutines: maxGoroutines,
			}
			return
		}
	}
}

func readThreadCount() (int, error) {
	f, err := os.Open("/proc/self/status")
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "Threads:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				break
			}
			n, parseErr := strconv.Atoi(fields[1])
			if parseErr != nil {
				return 0, parseErr
			}
			return n, nil
		}
		if scanErr := scanner.Err(); scanErr != nil {
			return 0, scanErr
		}
	}

	entries, dirErr := os.ReadDir("/proc/self/task")
	if dirErr != nil {
		if err != nil {
			return 0, fmt.Errorf("status=%v task=%w", err, dirErr)
		}
		return 0, dirErr
	}
	return len(entries), nil
}

func newSharedMmap(size int) ([]byte, func(), error) {
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

	return b, func() {
		if unmapErr := syscall.Munmap(b); unmapErr != nil {
			fmt.Fprintf(os.Stderr, "munmap failed: %v\n", unmapErr)
		}
	}, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
