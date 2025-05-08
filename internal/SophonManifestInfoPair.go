package internal

import (
	"fmt"
)

// SophonChunkManifestInfoPair holds information about chunks and manifests
type SophonChunkManifestInfoPair struct {
	ChunksInfo           *SophonChunksInfo
	ManifestInfo         *SophonManifestInfo
	OtherSophonBuildData *SophonManifestBuildData
	OtherSophonPatchData *SophonManifestPatchData
	IsFound              bool
	ReturnCode           int
	ReturnMessage        string
}

// NewSophonChunkManifestInfoPair creates a new SophonChunkManifestInfoPair with default values
func NewSophonChunkManifestInfoPair() *SophonChunkManifestInfoPair {
	return &SophonChunkManifestInfoPair{
		IsFound:    true,
		ReturnCode: 0,
	}
}

// GetOtherManifestInfoPair retrieves a manifest info pair based on the matching field
func (p *SophonChunkManifestInfoPair) GetOtherManifestInfoPair(matchingField string) (*SophonChunkManifestInfoPair, error) {
	if p.OtherSophonBuildData == nil || p.OtherSophonBuildData.ManifestIdentityList == nil {
		return nil, fmt.Errorf("OtherSophonBuildData or ManifestIdentityList is nil")
	}

	var sophonManifestIdentity *SophonManifestBuildIdentity
	// Equivalent to FirstOrDefault in C#
	for _, identity := range p.OtherSophonBuildData.ManifestIdentityList {
		if identity.MatchingField == matchingField {
			sophonManifestIdentity = &identity
			break
		}
	}

	if sophonManifestIdentity == nil {
		return nil, fmt.Errorf("sophon manifest with matching field: %s is not found", matchingField)
	}

	chunksInfo := CreateChunksInfo(
		sophonManifestIdentity.ChunksUrlInfo.UrlPrefix,
		sophonManifestIdentity.ChunkInfo.ChunkCount,
		sophonManifestIdentity.ChunkInfo.FileCount,
		sophonManifestIdentity.ChunksUrlInfo.IsCompressed,
		sophonManifestIdentity.ChunkInfo.UncompressedSize,
		sophonManifestIdentity.ChunkInfo.CompressedSize,
	)

	manifestInfo := CreateManifestInfo(
		sophonManifestIdentity.ManifestUrlInfo.UrlPrefix,
		sophonManifestIdentity.ManifestFileInfo.Checksum,
		sophonManifestIdentity.ManifestFileInfo.FileName,
		sophonManifestIdentity.ManifestUrlInfo.IsCompressed,
		sophonManifestIdentity.ManifestFileInfo.UncompressedSize,
		sophonManifestIdentity.ManifestFileInfo.CompressedSize,
	)

	return &SophonChunkManifestInfoPair{
		ChunksInfo:           chunksInfo,
		ManifestInfo:         manifestInfo,
		OtherSophonBuildData: p.OtherSophonBuildData,
		OtherSophonPatchData: p.OtherSophonPatchData,
		IsFound:              true,
	}, nil
}

// GetOtherPatchInfoPair retrieves a patch info pair based on the matching field and version
func (p *SophonChunkManifestInfoPair) GetOtherPatchInfoPair(matchingField string, versionUpdateFrom string) (*SophonChunkManifestInfoPair, error) {
	if p.OtherSophonPatchData == nil || p.OtherSophonPatchData.ManifestIdentityList == nil {
		return nil, fmt.Errorf("OtherSophonPatchData or ManifestIdentityList is nil")
	}

	var sophonPatchIdentity *SophonManifestPatchIdentity
	// Equivalent to FirstOrDefault in C#
	for _, identity := range p.OtherSophonPatchData.ManifestIdentityList {
		if identity.MatchingField == matchingField {
			sophonPatchIdentity = &identity
			break
		}
	}

	if sophonPatchIdentity == nil {
		return nil, fmt.Errorf("sophon patch with matching field: %s is not found", matchingField)
	}

	sophonChunkInfo, exists := sophonPatchIdentity.DiffTaggedInfo[versionUpdateFrom]
	if !exists {
		return nil, fmt.Errorf("sophon patch diff tagged info with tag: %s is not found", p.OtherSophonPatchData.TagName)
	}

	chunksInfo := CreateChunksInfo(
		sophonPatchIdentity.DiffUrlInfo.UrlPrefix,
		sophonChunkInfo.ChunkCount,
		sophonChunkInfo.FileCount,
		sophonPatchIdentity.DiffUrlInfo.IsCompressed,
		sophonChunkInfo.UncompressedSize,
		sophonChunkInfo.CompressedSize,
	)

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
		OtherSophonBuildData: p.OtherSophonBuildData,
		OtherSophonPatchData: p.OtherSophonPatchData,
		IsFound:              true,
	}, nil
}
