package client

import (
	"github.com/aws/aws-sdk-go-v2/aws"
)

// Client holds the shared AWS credentials and configuration
type Client struct {
	AwsConfig aws.Config
	Region    string
}
