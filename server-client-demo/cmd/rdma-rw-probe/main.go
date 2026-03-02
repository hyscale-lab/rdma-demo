package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsrdmahttp "github.com/aws/aws-sdk-go-v2/aws/transport/http/rdma"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func main() {
	var (
		endpoint       string
		region         string
		bucket         string
		key            string
		size           int
		requestTimeout time.Duration
	)

	flag.StringVar(&endpoint, "endpoint", "http://127.0.0.1:10190", "rdma s3 endpoint")
	flag.StringVar(&region, "region", "us-east-1", "s3 region")
	flag.StringVar(&bucket, "bucket", "bench-bucket", "bucket name")
	flag.StringVar(&key, "key", "rw-probe", "object key")
	flag.IntVar(&size, "size", 131072, "object size bytes")
	flag.DurationVar(&requestTimeout, "request-timeout", 10*time.Second, "per request timeout")
	flag.Parse()

	if size < 0 {
		log.Fatalf("size must be >= 0")
	}
	if requestTimeout <= 0 {
		log.Fatalf("request-timeout must be > 0")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}

	dialer := awsrdmahttp.NewVerbsDialer(awsrdmahttp.VerbsOptions{})
	dialer.DisableFallback = true

	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.Proxy = nil
	})

	cli := s3.New(s3.Options{
		Region:              region,
		BaseEndpoint:        aws.String(endpoint),
		UsePathStyle:        true,
		Credentials:         credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:          httpClient,
		EnableRDMATransport: true,
		RDMADialer:          dialer,
	})

	payload := bytes.Repeat([]byte{'x'}, size)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	err := ensureBucket(ctx, cli, bucket)
	cancel()
	if err != nil {
		log.Fatalf("ensure bucket: %v", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), requestTimeout)
	_, err = cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(payload),
		ContentLength: aws.Int64(int64(len(payload))),
	})
	cancel()
	if err != nil {
		log.Fatalf("put object: %v", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), requestTimeout)
	out, err := cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		cancel()
		log.Fatalf("get object: %v", err)
	}
	body, err := io.ReadAll(out.Body)
	_ = out.Body.Close()
	cancel()
	if err != nil {
		log.Fatalf("read body: %v", err)
	}

	if len(body) != len(payload) {
		log.Fatalf("size mismatch: put=%d get=%d", len(payload), len(body))
	}
	if !bytes.Equal(body, payload) {
		log.Fatalf("payload mismatch")
	}

	fmt.Printf("rw probe ok endpoint=%s size=%d key=%s\n", endpoint, size, key)
}

func ensureBucket(ctx context.Context, cli *s3.Client, bucket string) error {
	_, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
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

	_, headErr := cli.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if headErr == nil {
		return nil
	}
	return fmt.Errorf("create bucket: %w", err)
}
