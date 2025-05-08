package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// WriteUpdate updates an existing file by applying patches or reusing data from the old file
// This method handles sequential chunk updates, checking if chunks can be reused from an old file
// or needs to be downloaded from the network. It creates a temporary file during the process
// and renames it to the final name upon successful completion.
func (asset *SophonAsset) WriteUpdate(
	ctx context.Context,
	client *http.Client,
	oldInputDir string,
	newOutputDir string,
	chunkDir string,
	removeChunkAfterApply bool,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadCompleteDelegate DelegateDownloadAssetComplete,
) error {
	const tempExt = "_tempUpdate"

	// Validate inputs
	if err := EnsureOrThrowChunksState(asset); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, oldInputDir); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, newOutputDir); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, chunkDir); err != nil {
		return err
	}

	// Setup file paths
	outputOldPath := filepath.Join(oldInputDir, asset.AssetName)
	outputNewPath := filepath.Join(newOutputDir, asset.AssetName)
	outputNewTempPath := outputNewPath + tempExt
	outputNewDir := filepath.Dir(outputNewPath)

	// Create output directory if it doesn't exist
	if _, err := os.Stat(outputNewDir); os.IsNotExist(err) {
		if err := os.MkdirAll(outputNewDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	// Make sure files aren't read-only
	UnassignReadOnlyFromFileInfo(outputOldPath)
	UnassignReadOnlyFromFileInfo(outputNewPath)
	UnassignReadOnlyFromFileInfo(outputNewTempPath)

	// Process each chunk sequentially
	for _, chunk := range asset.Chunks {
		err := asset.innerWriteUpdate(
			ctx, client, chunkDir, writeInfoDelegate, downloadInfoDelegate,
			asset.DownloadSpeedLimiter, outputOldPath, outputNewTempPath,
			chunk, removeChunkAfterApply,
		)
		if err != nil {
			return err
		}
	}

	// Rename temp file to final file
	if outputNewTempPath != outputNewPath {
		// Remove existing file if it exists
		if _, err := os.Stat(outputNewPath); err == nil {
			if err := os.Remove(outputNewPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if err := os.Rename(outputNewTempPath, outputNewPath); err != nil {
			return fmt.Errorf("failed to rename temp file: %w", err)
		}
	}

	PushLogInfo(nil, fmt.Sprintf("Asset: %s | (Hash: %s -> %d bytes) has been completely updated!",
		asset.AssetName, asset.AssetHash, asset.AssetSize))

	if downloadCompleteDelegate != nil {
		downloadCompleteDelegate(asset)
	}

	return nil
}

// WriteUpdateParallel updates an existing file by applying patches in parallel
// This method performs the same function as WriteUpdate but processes chunks concurrently
// using multiple goroutines for improved performance. It's ideal for large files with
// many chunks that can be processed independently.
func (asset *SophonAsset) WriteUpdateParallel(
	ctx context.Context,
	client *http.Client,
	oldInputDir string,
	newOutputDir string,
	chunkDir string,
	removeChunkAfterApply bool,
	maxConcurrency int,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadCompleteDelegate DelegateDownloadAssetComplete,
) error {
	const tempExt = "_tempUpdate"

	// Validate inputs
	if err := EnsureOrThrowChunksState(asset); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, oldInputDir); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, newOutputDir); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, chunkDir); err != nil {
		return err
	}

	// Setup file paths
	outputOldPath := filepath.Join(oldInputDir, asset.AssetName)
	outputNewPath := filepath.Join(newOutputDir, asset.AssetName)
	outputNewTempPath := outputNewPath + tempExt
	outputNewDir := filepath.Dir(outputNewPath)

	// Create output directory if it doesn't exist
	if _, err := os.Stat(outputNewDir); os.IsNotExist(err) {
		if err := os.MkdirAll(outputNewDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	// Make sure files aren't read-only
	UnassignReadOnlyFromFileInfo(outputOldPath)
	UnassignReadOnlyFromFileInfo(outputNewPath)
	UnassignReadOnlyFromFileInfo(outputNewTempPath)

	// Check if output file already exists with correct size
	outputNewInfo, err := os.Stat(outputNewPath)
	if err == nil && outputNewInfo.Size() == asset.AssetSize {
		outputNewTempPath = outputNewPath
	}

	// Set default concurrency if not provided
	if maxConcurrency <= 0 {
		maxConcurrency = min(8, runtime.NumCPU())
	}

	// Create a context for cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create a semaphore to limit concurrency
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	// Process each chunk in parallel
	for _, chunk := range asset.Chunks {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore slot

		go func(c *SophonChunk) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			err := asset.innerWriteUpdate(
				ctx, client, chunkDir, writeInfoDelegate, downloadInfoDelegate,
				asset.DownloadSpeedLimiter, outputOldPath, outputNewTempPath,
				c, removeChunkAfterApply,
			)

			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel() // Cancel all other operations
				}
				errMu.Unlock()
			}
		}(chunk)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Check for errors
	if firstErr != nil {
		return firstErr
	}

	// Rename temp file to final file if they're different
	if outputNewTempPath != outputNewPath {
		// Ensure output directory exists
		if err := os.MkdirAll(filepath.Dir(outputNewPath), 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}

		// Remove existing file if it exists
		if _, err := os.Stat(outputNewPath); err == nil {
			if err := os.Remove(outputNewPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if err := os.Rename(outputNewTempPath, outputNewPath); err != nil {
			return fmt.Errorf("failed to rename temp file: %w", err)
		}
	}

	PushLogInfo(nil, fmt.Sprintf("Asset: %s | (Hash: %s -> %d bytes) has been completely updated!",
		asset.AssetName, asset.AssetHash, asset.AssetSize))

	if downloadCompleteDelegate != nil {
		downloadCompleteDelegate(asset)
	}

	return nil
}

// innerWriteUpdate handles updating a single chunk
// This helper method is the core of the update process for each chunk. It checks if:
// 1. The chunk can be reused from an old file (reference-based patching)
// 2. The chunk is available as a preloaded/cached file
// 3. The chunk needs to be downloaded
func (asset *SophonAsset) innerWriteUpdate(
	ctx context.Context,
	client *http.Client,
	chunkDir string,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
	outputOldPath string,
	outputNewPath string,
	chunk *SophonChunk,
	removeChunkAfterApply bool,
) error {
	var inputStream, outputStream io.ReadWriteSeeker
	var inputFile, outputFile *os.File
	var err error
	sourceType := SourceStreamType(Internet)

	defer func() {
		if inputFile != nil {
			inputFile.Close()
		}
		if outputFile != nil {
			outputFile.Close()
		}
	}()

	// Check if we can use data from old file
	isUseOldFile := chunk.ChunkOldOffset > -1
	if isUseOldFile {
		oldFileInfo, err := os.Stat(outputOldPath)
		if err == nil && oldFileInfo.Size() >= chunk.ChunkOldOffset+chunk.ChunkSizeDecompressed {
			inputFile, err = os.OpenFile(outputOldPath, os.O_RDWR, 0644)
			if err != nil {
				isUseOldFile = false
			} else {
				inputStream = inputFile
				sourceType = OldReference
				PushLogDebug(nil, fmt.Sprintf("Using old file as reference at offset: 0x%x -> 0x%x for: %s",
					chunk.ChunkOldOffset, chunk.ChunkSizeDecompressed, asset.AssetName))
			}
		} else {
			isUseOldFile = false
		}
	}

	// If we can't use old file, try cached chunk
	if !isUseOldFile {
		cachedChunkName := GetChunkStagingFilenameHash(chunk, asset)
		cachedChunkPath := filepath.Join(chunkDir, cachedChunkName)
		cachedChunkVerifyPath := cachedChunkPath + ".verified"

		cachedChunkInfo, err := os.Stat(cachedChunkPath)
		if err == nil {
			// Check if cached chunk has correct size
			if cachedChunkInfo.Size() != chunk.ChunkSize {
				if err := os.Remove(cachedChunkPath); err != nil {
					PushLogWarning(nil, fmt.Sprintf("Failed to remove invalid cached chunk: %v", err))
				}
				PushLogDebug(nil, fmt.Sprintf(
					"Cached/preloaded chunk has invalid size for: %s. Expecting: 0x%x but got: 0x%x instead. Falling back to download.",
					asset.AssetName, chunk.ChunkSize, cachedChunkInfo.Size()))
			} else {
				// Use cached chunk
				flag := os.O_RDONLY
				if removeChunkAfterApply {
					// Note: Go doesn't have a direct equivalent of FileOptions.DeleteOnClose
					// We'll remove the file after closing it
					defer os.Remove(cachedChunkPath)
				}

				inputFile, err = os.OpenFile(cachedChunkPath, flag, 0644)
				if err == nil {
					inputStream = inputFile
					sourceType = CachedLocal

					// Remove verify file if it exists
					if _, err := os.Stat(cachedChunkVerifyPath); err == nil {
						os.Remove(cachedChunkVerifyPath)
					}

					PushLogDebug(nil, fmt.Sprintf(
						"Using cached/preloaded chunk as reference at offset: 0x%x -> 0x%x for: %s",
						chunk.ChunkOffset, chunk.ChunkSizeDecompressed, asset.AssetName))
				}
			}
		}
	}

	// Open output file
	outputFile, err = os.OpenFile(outputNewPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	outputStream = outputFile

	// Perform the actual write
	return asset.performWriteStreamThread(
		ctx, client, inputStream, sourceType, outputStream, chunk,
		writeInfoDelegate, downloadInfoDelegate, downloadSpeedLimiter,
	)
}

// GetDownloadedPreloadSize calculates the total size of downloaded preload chunks
// This method examines all chunks for an asset and determines how much has already been
// downloaded, useful for showing progress indicators to users. It can return either
// compressed or decompressed size based on the useCompressedSize parameter.
func (asset *SophonAsset) GetDownloadedPreloadSize(
	ctx context.Context,
	chunkDir string,
	outputDir string,
	useCompressedSize bool,
) (int64, error) {
	// Check if the asset is already completely downloaded
	assetFullPath := filepath.Join(outputDir, asset.AssetName)
	assetInfo, err := os.Stat(assetFullPath)

	var assetDownloadedSize int64
	isAssetExist := err == nil
	if isAssetExist {
		assetDownloadedSize = assetInfo.Size()
	}

	// If no chunks, return 0
	if len(asset.Chunks) == 0 {
		return 0, nil
	}

	// Function to get the size of a downloaded chunk
	getLength := func(chunk *SophonChunk) int64 {
		cachedChunkName := GetChunkStagingFilenameHash(chunk, asset)
		cachedChunkPath := filepath.Join(chunkDir, cachedChunkName)

		// Get file info
		cachedChunkInfo, err := os.Stat(cachedChunkPath)
		chunkSizeToReturn := int64(0)

		if useCompressedSize {
			chunkSizeToReturn = chunk.ChunkSize
		} else {
			chunkSizeToReturn = chunk.ChunkSizeDecompressed
		}

		// If asset is fully downloaded, return 0 for each chunk
		if isAssetExist && assetDownloadedSize == asset.AssetSize && (err != nil || os.IsNotExist(err)) {
			return 0
		}

		// If chunk exists and has the right size, return its size
		if err == nil && cachedChunkInfo.Size() <= chunk.ChunkSize {
			return chunkSizeToReturn
		}

		return 0
	}

	// For small number of chunks, use sequential processing
	if len(asset.Chunks) < 512 {
		var total int64
		for _, chunk := range asset.Chunks {
			total += getLength(chunk)
		}
		return total, nil
	}

	// For large number of chunks, use parallel processing
	var total int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	numWorkers := runtime.NumCPU()
	chunksCopy := make([]*SophonChunk, len(asset.Chunks))
	copy(chunksCopy, asset.Chunks)

	// Divide work among workers
	chunkSize := (len(chunksCopy) + numWorkers - 1) / numWorkers

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()

			end := start + chunkSize
			if end > len(chunksCopy) {
				end = len(chunksCopy)
			}

			var subtotal int64
			for j := start; j < end; j++ {
				subtotal += getLength(chunksCopy[j])
			}

			mu.Lock()
			total += subtotal
			mu.Unlock()
		}(i * chunkSize)
	}

	wg.Wait()
	return total, nil
}
