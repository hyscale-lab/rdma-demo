package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	awsrdmahttp "github.com/hyscale-lab/rdma-demo/pkg/rdma"

	"github.com/hyscale-lab/rdma-demo/pkg/s3rdmasmoke"
)

const (
	defaultTransport         = "tcp"
	defaultBucket            = "bench-bucket"
	defaultKey               = "smoke-object"
	defaultOp                = "put-get"
	defaultPayloadSize       = 4096
	defaultRequestTimeout    = 30 * time.Second
	defaultRDMAConnectTO     = 10 * time.Second
	defaultRDMAControlTO     = 30 * time.Second
	defaultRDMADataTO        = 2 * time.Minute
	defaultRDMASharedMemSize = 16 << 20
	defaultRDMAPutOffset     = 0
	defaultRDMAGetOffset     = 8 << 20
	defaultRDMAGetMaxSize    = 8 << 20
)

func usageText(name string) string {
	if name == "" {
		name = "s3-rdma-smoke"
	}
	name = filepath.Base(name)

	var b strings.Builder
	fmt.Fprintf(&b, "Usage:\n  %s [flags]\n\n", name)
	b.WriteString("Combined smoke tool for the standalone s3-rdma-server.\n")
	b.WriteString("Use transport=tcp for the S3-compatible HTTP path or transport=rdma for the zcopy RDMA path.\n\n")
	b.WriteString("Common:\n")
	fmt.Fprintf(&b, "  --transport string\n\t\tSmoke transport: tcp or rdma. Default: %s.\n", defaultTransport)
	b.WriteString("  --endpoint value\n\t\tServer endpoint. Default depends on transport: tcp=http://127.0.0.1:10090, rdma=127.0.0.1:10191. For live RDMA runs, prefer an RDMA-backed interface address.\n")
	fmt.Fprintf(&b, "  --bucket string\n\t\tBucket name. Default: %s.\n", defaultBucket)
	fmt.Fprintf(&b, "  --key string\n\t\tObject key. Default: %s.\n", defaultKey)
	fmt.Fprintf(&b, "  --op string\n\t\tSmoke operation: put, get, or put-get. Default: %s.\n", defaultOp)
	b.WriteString("  --verify\n\t\tVerify the expected smoke semantics. Default: true.\n")
	fmt.Fprintf(&b, "  --payload-size bytes\n\t\tPayload size in bytes. Used for PUT data and GET size checks. Default: %d.\n", defaultPayloadSize)
	fmt.Fprintf(&b, "  --request-timeout duration\n\t\tPer-operation timeout for the TCP path. Default: %s.\n\n", defaultRequestTimeout)
	b.WriteString("RDMA-only:\n")
	fmt.Fprintf(&b, "  --rdma-connect-timeout duration\n\t\tTimeout for opening the RDMA connection. Default: %s.\n", defaultRDMAConnectTO)
	fmt.Fprintf(&b, "  --rdma-control-timeout duration\n\t\tTimeout for RDMA control operations such as hello and ensure-bucket. Default: %s.\n", defaultRDMAControlTO)
	fmt.Fprintf(&b, "  --rdma-data-timeout duration\n\t\tTimeout for RDMA PUT/GET payload operations. Default: %s.\n", defaultRDMADataTO)
	fmt.Fprintf(&b, "  --rdma-shared-memory-size bytes\n\t\tShared mmap region size. Default: %d.\n", defaultRDMASharedMemSize)
	fmt.Fprintf(&b, "  --rdma-put-offset bytes\n\t\tPUT payload offset inside the shared memory region. Default: %d.\n", defaultRDMAPutOffset)
	fmt.Fprintf(&b, "  --rdma-get-offset bytes\n\t\tGET target offset inside the shared memory region. Default: %d.\n", defaultRDMAGetOffset)
	fmt.Fprintf(&b, "  --rdma-get-max-size bytes\n\t\tMaximum readable GET size inside the shared memory region. Default: %d.\n", defaultRDMAGetMaxSize)
	b.WriteString("  --rdma-lowcpu\n\t\tPrefer lower CPU RDMA signaling behavior.\n")
	fmt.Fprintf(&b, "  --rdma-frame-payload bytes\n\t\tRDMA frame payload size. Default: %d.\n", awsrdmahttp.DefaultVerbsFramePayloadSize)
	fmt.Fprintf(&b, "  --rdma-send-depth int\n\t\tRDMA send queue depth. Default: %d.\n", awsrdmahttp.DefaultVerbsSendQueueDepth)
	fmt.Fprintf(&b, "  --rdma-recv-depth int\n\t\tRDMA recv queue depth. Default: %d.\n", awsrdmahttp.DefaultVerbsRecvQueueDepth)
	fmt.Fprintf(&b, "  --rdma-inline-threshold bytes\n\t\tRDMA inline threshold. Default: %d.\n", awsrdmahttp.DefaultVerbsInlineThreshold)
	fmt.Fprintf(&b, "  --rdma-send-signal-interval int\n\t\tRDMA send signal interval. Default: %d.\n", awsrdmahttp.DefaultVerbsSendSignalInterval)
	return b.String()
}

func main() {
	flag.CommandLine.Usage = func() {
		fmt.Fprint(os.Stderr, usageText(os.Args[0]))
	}

	transport := flag.String("transport", defaultTransport, "smoke transport: tcp or rdma")
	endpoint := flag.String("endpoint", "", "server endpoint (transport-specific default when empty)")
	bucket := flag.String("bucket", defaultBucket, "bucket name")
	key := flag.String("key", defaultKey, "object key")
	op := flag.String("op", defaultOp, "smoke operation: put, get, or put-get")
	verify := flag.Bool("verify", true, "verify expected smoke semantics")
	payloadSize := flag.Int("payload-size", defaultPayloadSize, "payload size in bytes")
	requestTimeout := flag.Duration("request-timeout", defaultRequestTimeout, "per-operation timeout for the TCP path")
	rdmaConnectTimeout := flag.Duration("rdma-connect-timeout", defaultRDMAConnectTO, "timeout for opening the RDMA connection")
	rdmaControlTimeout := flag.Duration("rdma-control-timeout", defaultRDMAControlTO, "timeout for RDMA control operations")
	rdmaDataTimeout := flag.Duration("rdma-data-timeout", defaultRDMADataTO, "timeout for RDMA PUT/GET payload operations")
	rdmaSharedMem := flag.Int("rdma-shared-memory-size", defaultRDMASharedMemSize, "RDMA shared mmap region size")
	rdmaPutOffset := flag.Int("rdma-put-offset", defaultRDMAPutOffset, "RDMA PUT offset in shared memory")
	rdmaGetOffset := flag.Int("rdma-get-offset", defaultRDMAGetOffset, "RDMA GET offset in shared memory")
	rdmaGetMaxSize := flag.Int("rdma-get-max-size", defaultRDMAGetMaxSize, "RDMA GET max size in shared memory")
	rdmaLowCPU := flag.Bool("rdma-lowcpu", false, "prefer lower CPU RDMA signaling behavior")
	rdmaFramePayload := flag.Int("rdma-frame-payload", awsrdmahttp.DefaultVerbsFramePayloadSize, "RDMA frame payload size")
	rdmaSendDepth := flag.Int("rdma-send-depth", awsrdmahttp.DefaultVerbsSendQueueDepth, "RDMA send queue depth")
	rdmaRecvDepth := flag.Int("rdma-recv-depth", awsrdmahttp.DefaultVerbsRecvQueueDepth, "RDMA recv queue depth")
	rdmaInline := flag.Int("rdma-inline-threshold", awsrdmahttp.DefaultVerbsInlineThreshold, "RDMA inline threshold")
	rdmaSignalIntvl := flag.Int("rdma-send-signal-interval", awsrdmahttp.DefaultVerbsSendSignalInterval, "RDMA send signal interval")
	flag.Parse()

	result, err := s3rdmasmoke.Run(context.Background(), s3rdmasmoke.Config{
		Transport:       *transport,
		Endpoint:        *endpoint,
		Bucket:          *bucket,
		Key:             *key,
		Op:              *op,
		Verify:          *verify,
		PayloadSize:     *payloadSize,
		RequestTimeout:  *requestTimeout,
		RDMAConnectTO:   *rdmaConnectTimeout,
		RDMAControlTO:   *rdmaControlTimeout,
		RDMADataTO:      *rdmaDataTimeout,
		RDMASharedMem:   *rdmaSharedMem,
		RDMAPutOffset:   *rdmaPutOffset,
		RDMAGetOffset:   *rdmaGetOffset,
		RDMAGetMaxSize:  *rdmaGetMaxSize,
		RDMALowCPU:      *rdmaLowCPU,
		RDMAFrameSize:   *rdmaFramePayload,
		RDMASendDepth:   *rdmaSendDepth,
		RDMARecvDepth:   *rdmaRecvDepth,
		RDMAInlineBytes: *rdmaInline,
		RDMASignalIntvl: *rdmaSignalIntvl,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println(result.Summary())
}
