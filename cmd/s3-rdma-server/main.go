package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	awsrdmahttp "github.com/hyscale-lab/rdma-demo/pkg/rdma"
	log "github.com/sirupsen/logrus"

	"github.com/hyscale-lab/rdma-demo/internal/s3rdmaserver/app"
)

const (
	defaultTCPListen       = "127.0.0.1:10090"
	defaultRDMAZCopyListen = "127.0.0.1:10191"
	defaultRegion          = "us-east-1"
	defaultMaxObjectSize   = 64 << 20
)

func usageText(name string) string {
	if name == "" {
		name = "s3-rdma-server"
	}
	name = filepath.Base(name)

	var b strings.Builder
	fmt.Fprintf(&b, "Usage:\n  %s [flags]\n\n", name)
	b.WriteString("Standalone benchmark server with minimal S3/TCP support and RDMA zcopy support.\n")
	b.WriteString("PUT payloads are received and discarded; only startup-loaded payload-root objects are readable via GET.\n\n")
	b.WriteString("General:\n")
	b.WriteString("  --debug\n\t\tEnable debug logging. Default: false.\n")
	fmt.Fprintf(&b, "  --region string\n\t\tRegion returned by the S3-compatible API. Default: %s.\n", defaultRegion)
	b.WriteString("  --payload-root path\n\t\tOptional startup preload directory for persistent GET objects. Default: \"\".\n")
	fmt.Fprintf(&b, "  --max-object-size bytes\n\t\tMaximum accepted PUT size. Default: %d. Values <= 0 disable the limit.\n\n", defaultMaxObjectSize)
	b.WriteString("Data listeners:\n")
	fmt.Fprintf(&b, "  --tcp-listen addr\n\t\tHTTP/TCP listener address. Default: %s. Empty disables TCP.\n", defaultTCPListen)
	b.WriteString("  --enable-rdma-zcopy\n\t\tEnable the RDMA zcopy listener. Default: false.\n")
	fmt.Fprintf(&b, "  --rdma-zcopy-listen addr\n\t\tRDMA zcopy listener address. Default: %s. For live RDMA traffic, use an RDMA-backed interface address.\n\n", defaultRDMAZCopyListen)
	b.WriteString("RDMA tuning:\n")
	fmt.Fprintf(&b, "  --rdma-backlog int\n\t\tRDMA listener backlog. Default: %d. A value of 0 uses the RDMA library default.\n", awsrdmahttp.DefaultVerbsListenBacklog)
	fmt.Fprintf(&b, "  --rdma-accept-workers int\n\t\tRDMA accept worker count. Default: %d. A value <= 0 uses the RDMA library default.\n", awsrdmahttp.DefaultVerbsAcceptWorkers)
	fmt.Fprintf(&b, "  --rdma-frame-payload int\n\t\tRDMA frame payload size in bytes. Default: %d.\n", awsrdmahttp.DefaultVerbsFramePayloadSize)
	fmt.Fprintf(&b, "  --rdma-send-depth int\n\t\tRDMA send queue depth. Default: %d.\n", awsrdmahttp.DefaultVerbsSendQueueDepth)
	fmt.Fprintf(&b, "  --rdma-recv-depth int\n\t\tRDMA recv queue depth. Default: %d.\n", awsrdmahttp.DefaultVerbsRecvQueueDepth)
	fmt.Fprintf(&b, "  --rdma-inline-threshold int\n\t\tRDMA inline threshold in bytes. Default: %d.\n", awsrdmahttp.DefaultVerbsInlineThreshold)
	b.WriteString("  --rdma-lowcpu\n\t\tPrefer fewer RDMA completion signals when the signal interval is left at 0.\n")
	fmt.Fprintf(&b, "  --rdma-send-signal-interval int\n\t\tRDMA send completion interval. Default: %d.\n", awsrdmahttp.DefaultVerbsSendSignalInterval)
	return b.String()
}

func main() {
	flag.CommandLine.Usage = func() {
		fmt.Fprint(os.Stderr, usageText(os.Args[0]))
	}

	debug := flag.Bool("debug", false, "enable debug logging")
	tcpListen := flag.String("tcp-listen", defaultTCPListen, "TCP listen address, empty disables TCP")
	enableRDMAZCopy := flag.Bool("enable-rdma-zcopy", false, "enable RDMA zcopy listener")
	rdmaZCopyListen := flag.String("rdma-zcopy-listen", defaultRDMAZCopyListen, "RDMA zcopy listen address")
	region := flag.String("region", defaultRegion, "region returned by the S3-compatible API")
	payloadRoot := flag.String("payload-root", "", "optional payload-root directory loaded at startup")
	maxObjectSize := flag.Int64("max-object-size", defaultMaxObjectSize, "maximum accepted PUT size in bytes; <=0 disables the limit")
	rdmaBacklog := flag.Int("rdma-backlog", awsrdmahttp.DefaultVerbsListenBacklog, "RDMA listener backlog")
	rdmaWorkers := flag.Int("rdma-accept-workers", awsrdmahttp.DefaultVerbsAcceptWorkers, "RDMA accept worker count")
	rdmaLowCPU := flag.Bool("rdma-lowcpu", false, "prefer lower CPU RDMA signaling defaults")
	rdmaFramePayload := flag.Int("rdma-frame-payload", awsrdmahttp.DefaultVerbsFramePayloadSize, "RDMA frame payload size in bytes")
	rdmaSendDepth := flag.Int("rdma-send-depth", awsrdmahttp.DefaultVerbsSendQueueDepth, "RDMA send queue depth")
	rdmaRecvDepth := flag.Int("rdma-recv-depth", awsrdmahttp.DefaultVerbsRecvQueueDepth, "RDMA recv queue depth")
	rdmaInline := flag.Int("rdma-inline-threshold", awsrdmahttp.DefaultVerbsInlineThreshold, "RDMA inline threshold in bytes")
	rdmaSignalIntvl := flag.Int("rdma-send-signal-interval", awsrdmahttp.DefaultVerbsSendSignalInterval, "RDMA send signal interval")
	flag.Parse()

	cfg := app.Config{
		Debug:            *debug,
		TCPListen:        *tcpListen,
		EnableRDMAZCopy:  *enableRDMAZCopy,
		RDMAZCopyListen:  *rdmaZCopyListen,
		RDMABacklog:      *rdmaBacklog,
		RDMAWorkers:      *rdmaWorkers,
		RDMALowCPU:       *rdmaLowCPU,
		RDMAFramePayload: *rdmaFramePayload,
		RDMASendDepth:    *rdmaSendDepth,
		RDMARecvDepth:    *rdmaRecvDepth,
		RDMAInline:       *rdmaInline,
		RDMASignalIntvl:  *rdmaSignalIntvl,
		Region:           *region,
		PayloadRoot:      *payloadRoot,
		MaxObjectSize:    *maxObjectSize,
	}

	if cfg.Debug {
		log.SetLevel(log.DebugLevel)
		log.Debug("debug mode enabled")
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
