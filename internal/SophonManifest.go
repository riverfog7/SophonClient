package internal

import (
	"context"
	"fmt"
	"net/http"

	"github.com/riverfog7/SophonClient/internal/protos"
	"google.golang.org/protobuf/proto"
)

// Enumerate fetches and yields Sophon assets from a manifest using the provided info pair
func Enumerate(ctx context.Context, httpClient *http.Client, infoPair *SophonChunkManifestInfoPair, downloadSpeedLimiter *SophonDownloadSpeedLimiter) (chan *SophonAsset, error) {
	return EnumerateWithInfo(ctx, httpClient, infoPair.ManifestInfo, infoPair.ChunksInfo, downloadSpeedLimiter)
}

// EnumerateWithInfo fetches and yields Sophon assets from a manifest using the provided manifest and chunks info
func EnumerateWithInfo(ctx context.Context, httpClient *http.Client, manifestInfo *SophonManifestInfo, chunksInfo *SophonChunksInfo, downloadSpeedLimiter *SophonDownloadSpeedLimiter) (chan *SophonAsset, error) {
	assetChan := make(chan *SophonAsset)

	// Define the task to fetch and parse the manifest
	task := func(ctx context.Context) (*protos.SophonManifestProto, error) {
		var manifestProto protos.SophonManifestProto
		err := ReadProtoFromManifestInfo(httpClient, manifestInfo, proto.Unmarshal, &manifestProto)
		if err != nil {
			return nil, fmt.Errorf("failed to read manifest proto: %w", err)
		}
		return &manifestProto, nil
	}

	// Execute with retry logic
	manifestProto, err := WaitForRetry(ctx, task, nil, nil, nil, nil)
	if err != nil {
		close(assetChan)
		return nil, err
	}

	// Start a goroutine to send assets to the channel
	go func() {
		defer close(assetChan)
		for _, asset := range manifestProto.Assets {
			if ctx.Err() != nil {
				return // Stop if context is canceled
			}
			sophonAsset := AssetProperty2SophonAsset(asset, chunksInfo, downloadSpeedLimiter)
			assetChan <- sophonAsset
		}
	}()

	return assetChan, nil
}

// AssetProperty2SophonAsset converts a manifest asset property to a SophonAsset
func AssetProperty2SophonAsset(asset *protos.SophonManifestAssetProperty, chunksInfo *SophonChunksInfo, downloadSpeedLimiter *SophonDownloadSpeedLimiter) *SophonAsset {
	if asset.AssetType != 0 || asset.AssetHashMd5 == "" {
		return &SophonAsset{
			AssetName:            asset.AssetName,
			IsDirectory:          true,
			DownloadSpeedLimiter: downloadSpeedLimiter,
		}
	}

	// Convert chunks
	chunks := make([]*SophonChunk, len(asset.AssetChunks))
	for i, chunkProp := range asset.AssetChunks {
		hash, err := HexToBytes(chunkProp.ChunkDecompressedHashMd5)
		if err != nil {
			// Handle error gracefully, perhaps log it
			hash = []byte{}
		}
		chunks[i] = &SophonChunk{
			ChunkName:             chunkProp.ChunkName,
			ChunkHashDecompressed: hash,
			ChunkOffset:           chunkProp.ChunkOnFileOffset,
			ChunkSize:             chunkProp.ChunkSize,
			ChunkSizeDecompressed: chunkProp.ChunkSizeDecompressed,
		}
	}

	return &SophonAsset{
		AssetName:            asset.AssetName,
		AssetHash:            asset.AssetHashMd5,
		AssetSize:            asset.AssetSize,
		Chunks:               chunks,
		SophonChunksInfo:     chunksInfo,
		IsDirectory:          false,
		DownloadSpeedLimiter: downloadSpeedLimiter,
	}
}
