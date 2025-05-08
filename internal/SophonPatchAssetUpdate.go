package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ApplyPatchUpdate applies a patch to update a file based on the patch method
// This is the main entry point for patch operations that determines which
// specific strategy to use (download, copy, patch, or remove) based on the PatchMethod.
// It also performs file verification to avoid unnecessary operations.
func (asset *SophonPatchAsset) ApplyPatchUpdate(
	ctx context.Context,
	client *http.Client,
	inputDir string,
	patchOutputDir string,
	removeOldAssets bool,
	downloadReadDelegate func(int64),
	diskWriteDelegate func(int64),
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
) error {
	// Determine source file path based on patch method
	var sourceFileNameToCheck string
	switch asset.PatchMethod {
	case Remove:
		sourceFileNameToCheck = asset.OriginalFilePath
	case DownloadOver:
		sourceFileNameToCheck = asset.TargetFilePath
	case Patch:
		sourceFileNameToCheck = asset.OriginalFilePath
	case CopyOver:
		sourceFileNameToCheck = asset.TargetFilePath
	default:
		return fmt.Errorf("unsupported patch method: %v", asset.PatchMethod)
	}

	sourceFilePathToCheck := filepath.Join(inputDir, sourceFileNameToCheck)

	// Handle file removal
	if asset.PatchMethod == Remove {
		if !removeOldAssets {
			return nil
		}

		_, err := os.Stat(sourceFilePathToCheck)
		if err == nil {
			return asset.performPatchAssetRemove(sourceFilePathToCheck)
		}
		return nil
	}

	// Check if file is already patched
	isPatched, err_ := asset.isFilePatched(ctx, inputDir)
	if err_ == nil && isPatched {
		if diskWriteDelegate != nil {
			diskWriteDelegate(asset.TargetFileSize)
		}
		return nil
	}

	// For CopyOver and Patch methods, verify original file
	if asset.PatchMethod != CopyOver {
		var sourceFileHashString string
		var sourceFileSizeToCheck int64

		switch asset.PatchMethod {
		case DownloadOver:
			sourceFileHashString = asset.TargetFileHash
			sourceFileSizeToCheck = asset.TargetFileSize
		case Patch:
			sourceFileHashString = asset.OriginalFileHash
			sourceFileSizeToCheck = asset.OriginalFileSize
		}

		// Create a chunk for verification
		sourceFileHash, err := HexToBytes(sourceFileHashString)
		if err != nil {
			return fmt.Errorf("failed to decode hash: %w", err)
		}

		sourceFileToCheckAsChunk := &SophonChunk{
			ChunkHashDecompressed: sourceFileHash,
			ChunkName:             sourceFileNameToCheck,
			ChunkOffset:           0,
			ChunkOldOffset:        -1,
			ChunkSize:             sourceFileSizeToCheck,
			ChunkSizeDecompressed: sourceFileSizeToCheck,
		}

		// Check if source file exists and has the correct size
		sourceFileInfo, err := os.Stat(sourceFilePathToCheck)
		isNeedCompleteDownload := err != nil || sourceFileInfo.Size() != sourceFileSizeToCheck

		// If the file exists with the right size, verify its hash
		if !isNeedCompleteDownload {
			sourceFile, err := os.OpenFile(sourceFilePathToCheck, os.O_RDWR, 0644)
			if err != nil {
				return fmt.Errorf("failed to open source file: %w", err)
			}
			defer sourceFile.Close()

			// Check hash using appropriate method
			var isHashVerified bool
			if len(sourceFileToCheckAsChunk.ChunkHashDecompressed) > 8 {
				isHashVerified, err = CheckChunkMd5HashAsync(
					sourceFileToCheckAsChunk,
					sourceFile,
					true,
				)
			} else {
				isHashVerified, err = CheckChunkXxh64HashAsync(
					sourceFileToCheckAsChunk,
					asset.OriginalFilePath,
					sourceFile,
					sourceFileToCheckAsChunk.ChunkHashDecompressed,
					true,
				)
			}

			if err != nil || !isHashVerified {
				isNeedCompleteDownload = true

				// Remove corrupted file
				sourceFile.Close()
				if err := asset.performPatchAssetRemove(sourceFilePathToCheck); err != nil {
					PushLogWarning(nil, fmt.Sprintf("Failed to remove corrupted file: %s - %v", sourceFilePathToCheck, err))
				}
			} else if asset.PatchMethod != Patch {
				// File is already correct, no need for further operations
				if diskWriteDelegate != nil {
					diskWriteDelegate(asset.TargetFileSize)
				}
				return nil
			}
		}

		// If original file is missing or corrupt, switch to DownloadOver method
		if isNeedCompleteDownload {
			asset.PatchMethod = DownloadOver
		}
	}

	// Perform the appropriate patch operation based on method
	var err error
	switch asset.PatchMethod {
	case DownloadOver:
		err = asset.performPatchDownloadOver(ctx, client, inputDir, downloadReadDelegate, diskWriteDelegate, downloadSpeedLimiter)
	case CopyOver:
		err = asset.performPatchCopyOver(ctx, inputDir, patchOutputDir, diskWriteDelegate)
	case Patch:
		err = asset.performPatchHDiff(ctx, inputDir, patchOutputDir, diskWriteDelegate)
	default:
		err = fmt.Errorf("invalid operation while performing patch: %v", asset.PatchMethod)
	}

	return err
}

// performPatchDownloadOver downloads a complete file when patching cannot be performed
// This method is used when a complete new version of a file is needed, either because
// the original is missing/corrupted or because a direct download is more efficient.
func (asset *SophonPatchAsset) performPatchDownloadOver(
	ctx context.Context,
	client *http.Client,
	inputDir string,
	downloadReadDelegate func(int64),
	diskWriteDelegate func(int64),
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
) error {
	// Set up target file paths
	targetFilePath := filepath.Join(inputDir, asset.TargetFilePath)
	targetFilePathTemp := targetFilePath + ".temp"

	// Ensure target directory exists
	if err := os.MkdirAll(filepath.Dir(targetFilePath), 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Make temporary file writable if it exists
	UnassignReadOnlyFromFileInfo(targetFilePathTemp)

	// Create temporary file
	tempFile, err := os.OpenFile(targetFilePathTemp, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	// Prepare for download
	targetChunkInfo := asset.PatchInfo.CopyWithNewBaseUrl(asset.TargetFileDownloadOverBaseUrl)
	targetFileChunk, err := asset.SophonPatchAssetAsChunk(false, true, false)
	if err != nil {
		return fmt.Errorf("failed to create chunk: %w", err)
	}

	// Perform download and write to temp file
	err = asset.innerWriteChunkCopy(
		ctx, client, tempFile, targetFileChunk, targetChunkInfo, targetChunkInfo,
		func(bytesWritten int64) {
			if diskWriteDelegate != nil {
				diskWriteDelegate(bytesWritten)
			}
		},
		func(bytesRead, bytesWritten int64) {
			if downloadReadDelegate != nil {
				downloadReadDelegate(bytesRead)
			}
		},
		downloadSpeedLimiter,
	)
	if err != nil {
		return err
	}

	// Close temp file before moving
	tempFile.Close()

	// Remove existing file if it exists
	if _, err := os.Stat(targetFilePath); err == nil {
		UnassignReadOnlyFromFileInfo(targetFilePath)
		if err := os.Remove(targetFilePath); err != nil {
			return fmt.Errorf("failed to remove existing file: %w", err)
		}
	}

	// Rename temp file to target file
	if err := os.Rename(targetFilePathTemp, targetFilePath); err != nil {
		return fmt.Errorf("failed to rename temporary file: %w", err)
	}

	PushLogDebug(nil, fmt.Sprintf("[Method: DownloadOver] Successfully updated file: %s", asset.TargetFilePath))
	return nil
}

// performPatchAssetRemove removes an asset file
// This method handles safely removing files, including making them writable first
// if they are read-only. It's used for cleanup operations during patching.
func (asset *SophonPatchAsset) performPatchAssetRemove(originalFilePath string) error {
	// Check if file exists
	_, err := os.Stat(originalFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Make file writable
	if err := UnassignReadOnlyFromFileInfo(originalFilePath); err != nil {
		PushLogWarning(nil, fmt.Sprintf("Failed to make file writable: %s - %v", originalFilePath, err))
	}

	// Delete the file
	if err := os.Remove(originalFilePath); err != nil {
		PushLogError(nil, fmt.Sprintf("An error has occurred while deleting old asset: %s | %v",
			originalFilePath, err))
		return err
	}

	PushLogDebug(nil, fmt.Sprintf("[Method: Remove] Removing asset file: %s is completed!", asset.OriginalFilePath))
	return nil
}

// performPatchCopyOver copies patch data directly to the target file
// This method is used when a file can be updated by directly copying new data
// from a patch file into the target location without any complex diffing.
func (asset *SophonPatchAsset) performPatchCopyOver(
	ctx context.Context,
	inputDir string,
	patchOutputDir string,
	diskWriteDelegate func(int64),
) error {
	// Create PatchTargetProperty (which coordinates file streams)
	property, err := newPatchTargetProperty(patchOutputDir, asset.PatchNameSource,
		inputDir, asset.TargetFilePath, asset.PatchOffset, asset.PatchChunkLength, true)
	if err != nil {
		return err
	}
	defer property.dispose()

	// Choose strategy based on file size
	useCopyToStrategy := asset.PatchChunkLength <= 1<<20 // 1MB
	logMessage := fmt.Sprintf(
		"[Method: CopyOver][Strategy: %s] Writing target file: %s with offset: 0x%x and length: 0x%x from %s is completed!",
		map[bool]string{true: "DirectCopyTo", false: "BufferedCopy"}[useCopyToStrategy],
		asset.TargetFilePath, asset.PatchOffset, asset.PatchChunkLength, asset.PatchNameSource)

	// Validate streams
	if property.targetFileTempStream == nil {
		return errors.New("target file temporary stream is null")
	}
	if property.patchChunkStream == nil {
		return errors.New("patch chunk stream is null")
	}

	// Use direct copy for small files
	if useCopyToStrategy {
		written, err := io.Copy(property.targetFileTempStream, property.patchChunkStream)
		if err != nil {
			return fmt.Errorf("failed to copy data: %w", err)
		}
		if diskWriteDelegate != nil {
			diskWriteDelegate(written)
		}
	} else {
		// Use buffered copy for larger files
		buffer := make([]byte, 16<<10) // 16KB buffer

		for {
			read, err := property.patchChunkStream.Read(buffer)
			if err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("failed to read from patch: %w", err)
			}

			if read == 0 {
				break
			}

			_, err = property.targetFileTempStream.Write(buffer[:read])
			if err != nil {
				return fmt.Errorf("failed to write to target: %w", err)
			}

			if diskWriteDelegate != nil {
				diskWriteDelegate(int64(read))
			}
		}
	}

	PushLogDebug(nil, logMessage)
	return nil
}

// performPatchHDiff applies an HDiff patch to update a file
// This method applies binary patching using the HDiff algorithm to
// transform an original file into a new version using patch data.
func (asset *SophonPatchAsset) performPatchHDiff(
	ctx context.Context,
	inputDir string,
	patchOutputDir string,
	diskWriteDelegate func(int64),
) error {
	// Create PatchTargetProperty
	property, err := newPatchTargetProperty(patchOutputDir, asset.PatchNameSource,
		inputDir, asset.TargetFilePath, asset.PatchOffset, asset.PatchChunkLength, false)
	if err != nil {
		return err
	}
	defer property.dispose()

	logMessage := fmt.Sprintf(
		"[Method: PatchHDiff] Writing target file: %s with offset: 0x%x and length: 0x%x from %s is completed!",
		asset.TargetFilePath, asset.PatchOffset, asset.PatchChunkLength, asset.PatchNameSource)

	patchPath := property.patchFilePath
	targetTempPath := property.targetFileTempInfo.Name()
	inputPath := filepath.Join(inputDir, asset.OriginalFilePath)

	// Apply HDiff patch
	// Note: This is a placeholder for HDiff functionality
	// In a complete implementation, you'd need to port SharpHDiffPatch to Go
	// or use a Go HDiff implementation
	err = applyHDiffPatch(inputPath, targetTempPath, patchPath, asset.PatchOffset, asset.PatchChunkLength, diskWriteDelegate)
	if err != nil {
		PushLogDebug(nil, fmt.Sprintf(
			"[Method: PatchHDiff] An error occurred while trying to perform patching on: %s -> %s\n%v",
			asset.OriginalFilePath, asset.TargetFilePath, err))
		return err
	}

	PushLogDebug(nil, logMessage)
	return nil
}

// isFilePatched checks if a file has already been patched
// This method verifies if a file already matches the expected target by checking
// its size and hash, helping to avoid unnecessary operations.
func (asset *SophonPatchAsset) isFilePatched(ctx context.Context, inputPath string) (bool, error) {
	targetFilePath := filepath.Join(inputPath, asset.TargetFilePath)

	// Check file size
	fileInfo, err := os.Stat(targetFilePath)
	if err != nil || fileInfo.Size() != asset.TargetFileSize {
		return false, errors.New("file size mismatch")
	}

	// Create chunk for verification
	targetHash, err := HexToBytes(asset.TargetFileHash)
	if err != nil {
		return false, err
	}

	checkByHashChunk := &SophonChunk{
		ChunkHashDecompressed: targetHash,
		ChunkName:             asset.TargetFilePath,
		ChunkSize:             asset.TargetFileSize,
		ChunkSizeDecompressed: asset.TargetFileSize,
		ChunkOffset:           0,
		ChunkOldOffset:        -1,
	}

	// Open file and verify hash
	file, err := os.OpenFile(targetFilePath, os.O_RDWR, 0644)
	if err != nil {
		return false, err
	}
	defer file.Close()

	var isHashMatched bool
	if len(checkByHashChunk.ChunkHashDecompressed) == 8 {
		isHashMatched, err = CheckChunkXxh64HashAsync(
			checkByHashChunk,
			asset.TargetFilePath,
			file,
			checkByHashChunk.ChunkHashDecompressed,
			true,
		)
	} else {
		isHashMatched, err = CheckChunkMd5HashAsync(
			checkByHashChunk,
			file,
			true,
		)
	}

	return isHashMatched, err
}

// patchTargetProperty manages file resources for patch operations
type patchTargetProperty struct {
	targetFileInfo       *os.FileInfo
	targetFileTempInfo   os.FileInfo
	targetFileTempStream *os.File
	patchFilePath        string
	patchFileStream      *os.File
	patchChunkStream     *ChunkStream
}

// newPatchTargetProperty creates a new patchTargetProperty instance
func newPatchTargetProperty(
	patchOutputDir string,
	patchNameSource string,
	inputDir string,
	targetFilePath string,
	patchOffset int64,
	patchLength int64,
	createStream bool,
) (*patchTargetProperty, error) {
	patchFilePath := filepath.Join(patchOutputDir, patchNameSource)
	fullTargetPath := filepath.Join(inputDir, targetFilePath)
	targetFileTempPath := fullTargetPath + ".temp"

	// Ensure directories exist
	if err := os.MkdirAll(filepath.Dir(targetFileTempPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Ensure temp file is writable if it exists
	UnassignReadOnlyFromFileInfo(targetFileTempPath)

	// Check if patch file exists
	if _, err := os.Stat(patchFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("required patch file: %s is not found", patchFilePath)
	}

	p := &patchTargetProperty{
		patchFilePath: patchFilePath,
	}

	// Optionally create streams
	if createStream {
		patchChunkEnd := patchOffset + patchLength

		// Create target temp file
		targetTempFile, err := os.OpenFile(targetFileTempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to create target temp file: %w", err)
		}
		p.targetFileTempStream = targetTempFile

		// Open patch file
		patchFile, err := os.Open(patchFilePath)
		if err != nil {
			targetTempFile.Close()
			return nil, fmt.Errorf("failed to open patch file: %w", err)
		}
		p.patchFileStream = patchFile

		// Create chunk stream
		chunkStream, err := NewChunkStream(patchFile, patchOffset, patchChunkEnd, false)
		if err != nil {
			targetTempFile.Close()
			patchFile.Close()
			return nil, fmt.Errorf("failed to create chunk stream: %w", err)
		}
		p.patchChunkStream = chunkStream
	}

	return p, nil
}

// dispose cleans up resources and finalizes the patch operation
func (p *patchTargetProperty) dispose() {
	// Close streams
	if p.patchChunkStream != nil {
		p.patchChunkStream.Close()
	}
	if p.patchFileStream != nil {
		p.patchFileStream.Close()
	}
	if p.targetFileTempStream != nil {
		p.targetFileTempStream.Close()
	}

	// Rename temp file to final file
	tempPath := p.targetFileTempStream.Name()
	finalPath := strings.TrimSuffix(tempPath, ".temp")

	// Remove existing file
	UnassignReadOnlyFromFileInfo(finalPath)
	os.Remove(finalPath)

	// Move temp file to final location
	os.Rename(tempPath, finalPath)
}

// applyHDiffPatch applies a binary diff to transform source file into target
// This is a placeholder for HDiff functionality - in a complete implementation,
// you would need a Go implementation of the HDiff patching algorithm
func applyHDiffPatch(
	sourcePath string,
	targetPath string,
	patchPath string,
	patchOffset int64,
	patchLength int64,
	progressCallback func(int64),
) error {
	// This is a simplified placeholder - actual implementation would require:
	// 1. Reading the source file
	// 2. Opening the patch file at the specified offset
	// 3. Applying the HDiff algorithm to generate the target file
	// 4. Reporting progress via the callback

	PushLogDebug(nil, "HDiff patching would happen here")
	return errors.New("HDiff patching not implemented in this port")
}
