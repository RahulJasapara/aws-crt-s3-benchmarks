module github.com/awslabs/aws-crt-s3-benchmarks/runners/s3-benchrunner-go

go 1.24

require (
	github.com/aws/aws-sdk-go-v2 v1.43.8
	github.com/aws/aws-sdk-go-v2/config v1.32.39
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.0.0-00010101000000-000000000000
	github.com/aws/aws-sdk-go-v2/service/s3 v1.107.4
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.19 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.38 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.39 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.39 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.39 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.40 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.18 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.32 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.39 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.40 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.8 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
)

// Build against the local (soon-to-be-optimized) copy of the SDK, not a
// published release. Adjust these paths if the SDK repo moves.
replace github.com/aws/aws-sdk-go-v2 => ../../../aws-sdk-go-v2

replace github.com/aws/aws-sdk-go-v2/config => ../../../aws-sdk-go-v2/config

replace github.com/aws/aws-sdk-go-v2/service/s3 => ../../../aws-sdk-go-v2/service/s3

replace github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager => ../../../aws-sdk-go-v2/feature/s3/transfermanager
