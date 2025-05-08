package internal

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
)

// BytesToHex converts a byte slice to a hexadecimal string
func BytesToHex(bytes []byte) string {
	return hex.EncodeToString(bytes)
}

// HexToBytes converts a hexadecimal string to a byte slice
func HexToBytes(hexStr string) ([]byte, error) {
	if len(hexStr) == 0 {
		return []byte{}, nil
	}
	if len(hexStr)%2 == 1 {
		return nil, fmt.Errorf("hex string must have even length")
	}
	return hex.DecodeString(hexStr)
}

// SophonPatchAssetAsChunk converts a SophonPatchAsset to a SophonChunk
func (asset *SophonPatchAsset) SophonPatchAssetAsChunk(fromOriginalFile, fromTargetFile, isCompressed bool) (*SophonChunk, error) {
	var hashStr, fileName string
	var fileSize int64

	if fromOriginalFile {
		hashStr = asset.OriginalFileHash
		fileName = asset.OriginalFilePath
		fileSize = asset.OriginalFileSize
	} else if fromTargetFile {
		hashStr = asset.TargetFileHash
		fileName = asset.TargetFilePath
		fileSize = asset.TargetFileSize
	} else {
		hashStr = asset.PatchHash
		fileName = asset.PatchNameSource
		fileSize = asset.PatchSize
	}

	hash, err := HexToBytes(hashStr)
	if err != nil {
		return nil, err
	}

	return &SophonChunk{
		ChunkHashDecompressed: hash,
		ChunkName:             fileName,
		ChunkOffset:           0,
		ChunkOldOffset:        -1, // Default value as in C# constructor
		ChunkSize:             fileSize,
		ChunkSizeDecompressed: fileSize,
	}, nil
}

// CheckChunkXxh64HashAsync verifies a chunk's XXH64 hash
func CheckChunkXxh64HashAsync(chunk *SophonChunk, assetName string, outStream io.ReadSeeker, chunkXxh64Hash []byte, isSingularStream bool) (bool, error) {
	if !isSingularStream {
		_, err := outStream.Seek(chunk.ChunkOffset, io.SeekStart)
		if err != nil {
			return false, err
		}
	}

	h := xxhash.New()
	limitReader := io.LimitReader(outStream, chunk.ChunkSizeDecompressed)

	_, err := io.Copy(h, limitReader)
	if err != nil {
		PushLogWarning(nil, fmt.Sprintf("An error occurred while checking XXH64 hash for chunk: %s | 0x%x -> L: 0x%x for: %s\n%v",
			chunk.ChunkName, chunk.ChunkOffset, chunk.ChunkSizeDecompressed, assetName, err))
		return false, err
	}

	// Compare hashes
	sum := h.Sum(nil)
	return bytes.Equal(sum, chunkXxh64Hash), nil
}

// CheckChunkMd5HashAsync verifies a chunk's MD5 hash
func CheckChunkMd5HashAsync(chunk *SophonChunk, outStream io.ReadSeeker, isSingularStream bool) (bool, error) {
	const bufferSize = 32 * 1024 // Equivalent to SophonAsset.BufferSize

	_, err := outStream.Seek(chunk.ChunkOffset, io.SeekStart)
	if err != nil {
		return false, err
	}

	h := md5.New()
	buffer := make([]byte, bufferSize)
	remain := chunk.ChunkSizeDecompressed

	for remain > 0 {
		toRead := int64(bufferSize)
		if remain < toRead {
			toRead = remain
		}

		read, err := io.ReadFull(outStream, buffer[:toRead])
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return false, err
		}
		if read == 0 {
			break
		}

		h.Write(buffer[:read])
		remain -= int64(read)
	}

	// Compare hashes
	sum := h.Sum(nil)
	return bytes.Equal(sum, chunk.ChunkHashDecompressed), nil
}

// GetChunkStagingFilenameHash generates a hash for a chunk's staging filename
func GetChunkStagingFilenameHash(chunk *SophonChunk, asset *SophonAsset) string {
	concatName := fmt.Sprintf("%s$%s$%s", asset.AssetName, asset.AssetHash, chunk.ChunkName)
	h := xxhash.New()
	h.Write([]byte(concatName))
	return BytesToHex(h.Sum(nil))
}

// TryGetChunkXxh64Hash attempts to extract the XXH64 hash from a filename
func TryGetChunkXxh64Hash(fileName string) ([]byte, bool) {
	parts := strings.Split(fileName, "_")
	if len(parts) != 2 {
		return nil, false
	}

	if len(parts[0]) != 16 {
		return nil, false
	}

	hash, err := HexToBytes(parts[0])
	if err != nil {
		return nil, false
	}

	return hash, true
}

// EnsureOrThrowOutputDirectoryExistence checks if a directory exists
func EnsureOrThrowOutputDirectoryExistence(asset *SophonAsset, outputDirPath string) error {
	if outputDirPath == "" {
		return errors.New("directory path cannot be empty or null")
	}

	info, err := os.Stat(outputDirPath)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("directory path: %s does not exist", outputDirPath)
	}

	return nil
}

// EnsureOrThrowChunksState checks if an asset has chunks
func EnsureOrThrowChunksState(asset *SophonAsset) error {
	if asset.Chunks == nil {
		return errors.New("this asset does not have chunk(s)")
	}
	return nil
}

// EnsureOrThrowStreamState checks if a stream is valid for operations
func EnsureOrThrowStreamState(asset *SophonAsset, outStream io.ReadWriteSeeker) error {
	if outStream == nil {
		return errors.New("output stream cannot be null")
	}

	// In Go, we can't directly check if a stream is readable/writable/seekable
	// We'll assume the interface implementation guarantees these capabilities
	return nil
}

// UnassignReadOnlyFromFileInfo removes the read-only flag from a file
func UnassignReadOnlyFromFileInfo(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	if info.Mode()&0200 == 0 { // Check if write permission is not set
		// Make the file writable
		return os.Chmod(filePath, info.Mode()|0200)
	}

	return nil
}

// GetChunkAndIfAltAsync fetches a chunk with fallback to alternative source
func GetChunkAndIfAltAsync(httpClient *http.Client, chunkName string,
	currentSophonChunkInfo, altSophonChunkInfo *SophonChunksInfo) (*http.Response, error) {

	// Concat the string
	url := strings.TrimRight(currentSophonChunkInfo.ChunksBaseUrl, "/") + "/" + chunkName

	// Try to get the HttpResponseMessage
	resp, err := httpClient.Get(url)
	if err != nil {
		if altSophonChunkInfo == nil {
			return nil, err
		}

		// Try with alternative source
		return GetChunkAndIfAltAsync(httpClient, chunkName, altSophonChunkInfo, nil)
	}

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()

		if altSophonChunkInfo == nil {
			return nil, fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
		}

		// Try with alternative source
		return GetChunkAndIfAltAsync(httpClient, chunkName, altSophonChunkInfo, nil)
	}

	return resp, nil
}

// ReadProtoFromManifestInfo reads and parses a protobuf message from a manifest
func ReadProtoFromManifestInfo(httpClient *http.Client, manifestInfo *SophonManifestInfo,
	unmarshal func([]byte, proto.Message) error, message proto.Message) error {

	// Get the manifest file
	resp, err := httpClient.Get(manifestInfo.ManifestFileUrl())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body

	// Handle decompression if needed
	if manifestInfo.IsUseCompression {
		zReader, err := zstd.NewReader(resp.Body)
		if err != nil {
			return err
		}
		defer zReader.Close()
		reader = zReader
	}

	// Read the entire content
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	// Parse the protobuf message
	return unmarshal(data, message)
}

// ToSet converts a slice to a set (map with empty struct values)
func ToSet[T comparable](items []T) map[T]struct{} {
	set := make(map[T]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}
