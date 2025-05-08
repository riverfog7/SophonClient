package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SophonPatch provides methods related to Sophon patch manifests
type SophonPatch struct {
	Client *http.Client
}

// NewSophonPatch creates a new SophonPatch instance
func NewSophonPatch(client *http.Client) *SophonPatch {
	if client == nil {
		client = http.DefaultClient
	}
	return &SophonPatch{Client: client}
}

// CreateSophonChunkManifestInfoPair fetches patch manifest info and returns a SophonChunkManifestInfoPair
func (sp *SophonPatch) CreateSophonChunkManifestInfoPair(ctx context.Context, url string, versionUpdateFrom string, matchingField *string) (*SophonChunkManifestInfoPair, error) {
	// Use default matchingField if nil or empty
	if matchingField == nil || *matchingField == "" {
		defaultField := "game"
		matchingField = &defaultField
	}

	// Fetch patch branch info
	// Assuming GetSophonBranchInfoPatch is implemented to fetch SophonManifestPatchBranch
	sophonPatchBranch, err := sp.GetSophonBranchInfoPatch(ctx, url, http.MethodPost)
	if err != nil {
		return nil, fmt.Errorf("failed to get patch branch info: %w", err)
	}

	// Check if Data is nil
	if sophonPatchBranch.Data == nil {
		return &SophonChunkManifestInfoPair{
			IsFound:       false,
			ReturnCode:    sophonPatchBranch.ReturnCode,
			ReturnMessage: sophonPatchBranch.ReturnMessage,
		}, nil
	}

	// Find matching patch identity
	var sophonPatchIdentity *SophonManifestPatchIdentity
	for _, identity := range sophonPatchBranch.Data.ManifestIdentityList {
		if identity.MatchingField == *matchingField {
			sophonPatchIdentity = &identity
			break
		}
	}

	if sophonPatchIdentity == nil {
		return &SophonChunkManifestInfoPair{
			IsFound:       false,
			ReturnCode:    404,
			ReturnMessage: fmt.Sprintf("Sophon patch with matching field: %s is not found!", *matchingField),
		}, nil
	}

	// Check for versionUpdateFrom in DiffTaggedInfo
	sophonChunkInfo, ok := sophonPatchIdentity.DiffTaggedInfo[versionUpdateFrom]
	if !ok {
		return &SophonChunkManifestInfoPair{
			IsFound:       false,
			ReturnCode:    404,
			ReturnMessage: fmt.Sprintf("Sophon patch diff tagged info with version: %s is not found!", versionUpdateFrom),
		}, nil
	}

	// Create ChunksInfo
	chunksInfo := CreateChunksInfo(
		sophonPatchIdentity.DiffUrlInfo.UrlPrefix,
		sophonChunkInfo.ChunkCount,
		sophonChunkInfo.FileCount,
		sophonPatchIdentity.DiffUrlInfo.IsCompressed,
		sophonChunkInfo.UncompressedSize,
		sophonChunkInfo.CompressedSize,
	)

	// Create ManifestInfo
	manifestInfo := CreateManifestInfo(
		sophonPatchIdentity.ManifestUrlInfo.UrlPrefix,
		sophonPatchIdentity.ManifestFileInfo.Checksum,
		sophonPatchIdentity.ManifestFileInfo.FileName,
		sophonPatchIdentity.ManifestUrlInfo.IsCompressed,
		sophonPatchIdentity.ManifestFileInfo.UncompressedSize,
		sophonPatchIdentity.ManifestFileInfo.CompressedSize,
	)

	return &SophonChunkManifestInfoPair{
		ChunksInfo:           chunksInfo,
		ManifestInfo:         manifestInfo,
		OtherSophonBuildData: nil,
		OtherSophonPatchData: sophonPatchBranch.Data,
		IsFound:              true,
	}, nil
}

// GetSophonBranchInfoPatch is a placeholder for fetching SophonManifestPatchBranch
// This method should be implemented similar to GetSophonBranchInfo but for patch data
func (sp *SophonPatch) GetSophonBranchInfoPatch(ctx context.Context, url string, httpMethod string) (*SophonManifestPatchBranch, error) {
	req, err := http.NewRequestWithContext(ctx, httpMethod, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := sp.Client.Do(req)
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

	var branch SophonManifestPatchBranch
	if err := json.Unmarshal(body, &branch); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &branch, nil
}
