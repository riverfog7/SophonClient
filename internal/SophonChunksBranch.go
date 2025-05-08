package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SophonHTTPClient contains HTTP client methods for Sophon operations
type SophonHTTPClient struct {
	Client *http.Client
}

// NewSophonHTTPClient creates a new SophonHTTPClient instance
func NewSophonHTTPClient(client *http.Client) *SophonHTTPClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &SophonHTTPClient{Client: client}
}

// GetSophonBranchInfo retrieves branch information from the Sophon API
func (c *SophonHTTPClient) GetSophonBranchInfo(ctx context.Context, url string, httpMethod string) (*SophonManifestBuildBranch, error) {
	req, err := http.NewRequestWithContext(ctx, httpMethod, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var branch SophonManifestBuildBranch
	if err := json.Unmarshal(body, &branch); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &branch, nil
}

// CreateSophonChunkManifestInfoPair creates a chunk/manifest info pair
func (c *SophonHTTPClient) CreateSophonChunkManifestInfoPair(ctx context.Context, url string, matchingField string) (*SophonChunkManifestInfoPair, error) {
	if matchingField == "" {
		matchingField = "game"
	}

	branch, err := c.GetSophonBranchInfo(ctx, url, http.MethodGet)
	if err != nil {
		return nil, fmt.Errorf("failed to get branch info: %w", err)
	}

	if branch.Data == nil {
		return &SophonChunkManifestInfoPair{
			IsFound:       false,
			ReturnCode:    branch.ReturnCode,
			ReturnMessage: branch.ReturnMessage,
		}, nil
	}

	// Find first matching manifest identity
	var manifestIdentity *SophonManifestBuildIdentity
	for _, identity := range branch.Data.ManifestIdentityList {
		if identity.MatchingField == matchingField {
			manifestIdentity = &identity
			break
		}
	}

	if manifestIdentity == nil {
		return nil, fmt.Errorf("sophon manifest with matching field: %s not found", matchingField)
	}

	// Create chunks info
	chunksInfo := CreateChunksInfo(
		manifestIdentity.ChunksUrlInfo.UrlPrefix,
		manifestIdentity.ChunkInfo.ChunkCount,
		manifestIdentity.ChunkInfo.FileCount,
		manifestIdentity.ChunksUrlInfo.IsCompressed,
		manifestIdentity.ChunkInfo.UncompressedSize,
		manifestIdentity.ChunkInfo.CompressedSize,
	)

	// Create manifest info
	manifestInfo := CreateManifestInfo(
		manifestIdentity.ManifestUrlInfo.UrlPrefix,
		manifestIdentity.ManifestFileInfo.Checksum,
		manifestIdentity.ManifestFileInfo.FileName,
		manifestIdentity.ManifestUrlInfo.IsCompressed,
		manifestIdentity.ManifestFileInfo.UncompressedSize,
		manifestIdentity.ManifestFileInfo.CompressedSize,
	)

	return &SophonChunkManifestInfoPair{
		ChunksInfo:           chunksInfo,
		ManifestInfo:         manifestInfo,
		OtherSophonBuildData: branch.Data,
		IsFound:              true,
	}, nil
}
