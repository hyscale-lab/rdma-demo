module rdma-demo/server-client-demo

go 1.23.0

replace github.com/aws/aws-sdk-go-v2 => ../aws-sdk-go-v2

replace github.com/aws/aws-sdk-go-v2/service/s3 => ../aws-sdk-go-v2/service/s3

require (
	github.com/aws/aws-sdk-go-v2 v1.41.1
	github.com/aws/aws-sdk-go-v2/credentials v1.19.9
	github.com/aws/aws-sdk-go-v2/service/s3 v0.0.0-00010101000000-000000000000
	github.com/aws/smithy-go v1.24.0
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.17 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.17 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.17 // indirect
)
