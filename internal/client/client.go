package client

import (
	"github.com/aws/aws-sdk-go-v2/aws"

	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Client holds the shared AWS credentials and configuration
type Client struct {
	DevopsClient  *devopsagent.Client
	SecretsClient *secretsmanager.Client
	Config        aws.Config
	Region        string
}

const (
	// signingName is the SigV4 service signing name used by this API -
	// confirmed from a working CLI call's Authorization header:
	// Credential=.../.../aidevops/aws4_request
	signingName = "aidevops"
)

// devopsAgentHost returns the control-plane host for the configured region.
// Confirmed pattern from CLI --debug output: host_prefix "cp." + aidevops.<region>.api.aws
func devopsAgentHost(region string) string {
	return fmt.Sprintf("cp.aidevops.%s.api.aws", region)
}

// RawPost signs and sends a POST request directly against the DevOps Agent
// API, for request fields the published SDK model doesn't support yet
// (e.g. webhookAuthType on associate-service). Returns the raw response body;
// callers unmarshal into whatever shape they expect.
func RawPost(ctx context.Context, cfg aws.Config, path string, body []byte) ([]byte, error) {
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieving credentials: %w", err)
	}

	url := fmt.Sprintf("https://%s%s", devopsAgentHost(cfg.Region), path)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	payloadHash := sha256.Sum256(body)
	signer := v4.NewSigner()
	if err := signer.SignHTTP(
		ctx,
		creds,
		httpReq,
		hex.EncodeToString(payloadHash[:]),
		signingName,
		cfg.Region,
		time.Now(),
	); err != nil {
		return nil, fmt.Errorf("signing request: %w", err)
	}

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("request to %s returned status %d: %s", path, httpResp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}
