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
	"sync/atomic"
	"time"
)

// DownloadDiffChunks downloads staged chunks used as new data or data diff for preload and update
func (asset *SophonAsset) DownloadDiffChunks(
	client *http.Client,
	chunkDirOutput string,
	maxConcurrency int,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadCompleteDelegate DelegateDownloadAssetComplete,
	forceVerification bool,
) error {
	// Ensure asset has chunks and output directory exists
	if err := EnsureOrThrowChunksState(asset); err != nil {
		return err
	}
	if err := EnsureOrThrowOutputDirectoryExistence(asset, chunkDirOutput); err != nil {
		return err
	}

	// Initialize counters
	countChunksDownload := len(asset.Chunks)
	currentChunksDownloadPos := int32(0)
	currentChunksDownloadQueue := int32(0)

	// Set default concurrency if not provided
	if maxConcurrency <= 0 {
		maxConcurrency = min(8, runtime.NumCPU())
	}

	// Create a context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a semaphore to limit concurrency
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	// Process each chunk
	for _, chunk := range asset.Chunks {
		// Skip chunks that have an old offset
		if chunk.ChunkOldOffset > -1 {
			continue
		}

		// Add to waitgroup
		wg.Add(1)

		// Acquire semaphore slot
		sem <- struct{}{}

		// Start goroutine for this chunk
		go func(c *SophonChunk) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore slot

			// Process the chunk
			err := asset.performWriteDiffChunksThread(
				ctx,
				client,
				chunkDirOutput,
				c,
				writeInfoDelegate,
				downloadInfoDelegate,
				asset.DownloadSpeedLimiter,
				forceVerification,
				&currentChunksDownloadPos,
				&currentChunksDownloadQueue,
				countChunksDownload,
			)

			// If error occurred, store it and cancel context
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

	// If no errors and download complete delegate is provided, call it
	if firstErr == nil && downloadCompleteDelegate != nil {
		PushLogInfo(nil, fmt.Sprintf("Asset: %s | (Hash: %s -> %d bytes) has been completely downloaded!",
			asset.AssetName, asset.AssetHash, asset.AssetSize))
		downloadCompleteDelegate(asset)
	}

	return firstErr
}

// performWriteDiffChunksThread handles downloading and verifying a single chunk
func (asset *SophonAsset) performWriteDiffChunksThread(
	ctx context.Context,
	client *http.Client,
	chunkDirOutput string,
	chunk *SophonChunk,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
	forceVerification bool,
	currentChunksDownloadPos *int32,
	currentChunksDownloadQueue *int32,
	countChunksDownload int,
) error {
	// Generate chunk filename hash
	chunkNameHashed := GetChunkStagingFilenameHash(chunk, asset)
	chunkFilePathHashed := filepath.Join(chunkDirOutput, chunkNameHashed)
	chunkFileCheckedPath := chunkFilePathHashed + ".verified"

	// Increment counters
	atomic.AddInt32(currentChunksDownloadPos, 1)
	atomic.AddInt32(currentChunksDownloadQueue, 1)
	defer atomic.AddInt32(currentChunksDownloadQueue, -1)

	// Open file
	_, err := os.Stat(chunkFilePathHashed)
	if err == nil {
		// Remove read-only flag if exists
		UnassignReadOnlyFromFileInfo(chunkFilePathHashed)
	}

	file, err := os.OpenFile(chunkFilePathHashed, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open chunk file: %w", err)
	}
	defer file.Close()

	// Check if chunk needs to be downloaded
	isChunkUnmatch := true
	fileSize, _ := file.Seek(0, io.SeekEnd)
	isChunkUnmatch = fileSize != chunk.ChunkSize

	// Check if chunk is already verified
	isChunkVerified := false
	if _, err := os.Stat(chunkFileCheckedPath); err == nil && !isChunkUnmatch && !forceVerification {
		isChunkVerified = true
	}

	// Verify chunk if needed
	if !isChunkVerified {
		// Get chunk hash
		hash, hashExists := TryGetChunkXxh64Hash(chunk.ChunkName)
		if hashExists {
			// Reset file position
			file.Seek(0, io.SeekStart)
			// Check hash
			isMatch, err := CheckChunkXxh64HashAsync(chunk, asset.AssetName, file, hash, true)
			if err != nil {
				return err
			}
			isChunkUnmatch = !isMatch
		}

		// Remove verification file if it exists
		os.Remove(chunkFileCheckedPath)
	}

	// If chunk is valid, skip download
	if !isChunkUnmatch {
		currentPos := atomic.LoadInt32(currentChunksDownloadPos)
		currentQueue := atomic.LoadInt32(currentChunksDownloadQueue)
		PushLogDebug(nil, fmt.Sprintf("[%d/%d Queue: %d] Skipping chunk 0x%x -> L: 0x%x for: %s",
			currentPos, countChunksDownload, currentQueue, chunk.ChunkOffset, chunk.ChunkSizeDecompressed, asset.AssetName))

		// Call delegates
		if writeInfoDelegate != nil {
			writeInfoDelegate(chunk.ChunkSize)
		}
		if downloadInfoDelegate != nil {
			downloadInfoDelegate(chunk.ChunkSize, 0)
		}

		// Create verification file
		verifyFile, _ := os.Create(chunkFileCheckedPath)
		if verifyFile != nil {
			verifyFile.Close()
		}
		return nil
	}

	// Reset file position for download
	file.Seek(0, io.SeekStart)

	// Download chunk
	return asset.innerWriteChunkCopy(
		ctx,
		client,
		file,
		chunk,
		writeInfoDelegate,
		downloadInfoDelegate,
		downloadSpeedLimiter,
		currentChunksDownloadPos,
		currentChunksDownloadQueue,
		countChunksDownload,
	)
}

// innerWriteChunkCopy downloads a chunk from the server and writes it to the output stream
func (asset *SophonAsset) innerWriteChunkCopy(
	ctx context.Context,
	client *http.Client,
	outStream io.ReadWriteSeeker,
	chunk *SophonChunk,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
	currentChunksDownloadPos *int32,
	currentChunksDownloadQueue *int32,
	countChunksDownload int,
) error {
	const retryCount = DefaultRetryAttempt
	currentRetry := 0
	currentWriteOffset := int64(0)

	// Speed limiting setup
	var thisInstanceDownloadLimitBase int64 = -1
	if downloadSpeedLimiter != nil && downloadSpeedLimiter.InitialRequestedSpeed != nil {
		thisInstanceDownloadLimitBase = *downloadSpeedLimiter.InitialRequestedSpeed
	}

	// Throttling variables
	written := int64(0)
	startTime := time.Now()
	var maximumBytesPerSecond float64
	var bitPerUnit float64

	// Calculate bandwidth limits based on active downloads
	calculateBps := func() {
		if thisInstanceDownloadLimitBase <= 0 {
			thisInstanceDownloadLimitBase = -1
			maximumBytesPerSecond = -1
			return
		}

		// Minimum speed limit (64KB)
		thisInstanceDownloadLimitBase = max(int64(64<<10), thisInstanceDownloadLimitBase)

		// Get number of concurrent downloads
		threadNum := float64(1)
		if downloadSpeedLimiter != nil {
			threadNum = float64(downloadSpeedLimiter.GetCurrentChunkProcessing())
			if threadNum < 1 {
				threadNum = 1
			} else if threadNum > float64(16<<10) {
				threadNum = float64(16 << 10)
			}
		}

		maximumBytesPerSecond = float64(thisInstanceDownloadLimitBase) / threadNum
		bitPerUnit = 940.0 - (threadNum-2.0)/(16.0-2.0)*400.0
	}

	// Initial calculation
	calculateBps()

	// Set up event handlers
	if downloadSpeedLimiter != nil {
		downloadSpeedLimiter.IncrementChunkProcessedCount()
		defer downloadSpeedLimiter.DecrementChunkProcessedCount()
	}

	// Throttle function to limit download speed
	throttle := func() error {
		if maximumBytesPerSecond <= 0 || written <= 0 {
			return nil
		}

		elapsedMs := time.Since(startTime).Milliseconds()
		if elapsedMs > 0 {
			// Calculate current speed
			bps := float64(written) * bitPerUnit / float64(elapsedMs)

			// If exceeding limit, sleep
			if bps > maximumBytesPerSecond {
				wakeElapsed := float64(written) * bitPerUnit / maximumBytesPerSecond
				toSleep := wakeElapsed - float64(elapsedMs)

				if toSleep > 1 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Duration(toSleep) * time.Millisecond):
					}

					// Reset counters after sleep
					startTime = time.Now()
					written = 0
				}
			}
		}
		return nil
	}

	// Main download loop with retries
	for {
		// Reset file position
		if _, err := outStream.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek output stream: %w", err)
		}

		// Log download attempt
		PushLogDebug(nil, fmt.Sprintf("[%d/%d Queue: %d] Downloading chunk: %s | offset: 0x%x -> size: 0x%x",
			atomic.LoadInt32(currentChunksDownloadPos), countChunksDownload,
			atomic.LoadInt32(currentChunksDownloadQueue), chunk.ChunkName,
			chunk.ChunkOffset, chunk.ChunkSizeDecompressed))

		// Download chunk
		resp, err := GetChunkAndIfAltAsync(client, chunk.ChunkName, asset.SophonChunksInfo, asset.SophonChunksInfoAlt)
		if err != nil {
			if currentRetry < retryCount {
				// Report negative progress for retry
				if writeInfoDelegate != nil {
					writeInfoDelegate(-currentWriteOffset)
				}
				if downloadInfoDelegate != nil {
					downloadInfoDelegate(-currentWriteOffset, 0)
				}

				currentWriteOffset = 0
				currentRetry++

				PushLogWarning(nil, fmt.Sprintf(
					"Error downloading chunk: %s | Retry %d/%d: %v",
					chunk.ChunkName, currentRetry, retryCount, err))

				// Wait before retry
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
				continue
			}

			// Max retries exceeded
			return fmt.Errorf("failed to download chunk after %d attempts: %w", retryCount, err)
		}
		defer resp.Body.Close()

		// Create buffer for downloading
		buffer := make([]byte, asset.BufferSize)

		// Download and write loop
		for {
			read, err := resp.Body.Read(buffer)
			if err != nil && err != io.EOF {
				break // Will trigger retry
			}

			if read == 0 {
				break // End of response
			}

			// Write to output file
			_, err = outStream.Write(buffer[:read])
			if err != nil {
				break // Will trigger retry
			}

			// Update progress
			currentWriteOffset += int64(read)
			written += int64(read)

			// Report progress
			if writeInfoDelegate != nil {
				writeInfoDelegate(int64(read))
			}
			if downloadInfoDelegate != nil {
				downloadInfoDelegate(int64(read), int64(read))
			}

			// Reset retry counter as we're making progress
			currentRetry = 0

			// Apply throttling
			if err := throttle(); err != nil {
				return err
			}

			// Check for cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		// Verify hash of downloaded data
		_, err = outStream.Seek(0, io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek for verification: %w", err)
		}

		var isHashVerified bool

		// Try XXH64 hash first
		hash, hasXxHash := TryGetChunkXxh64Hash(chunk.ChunkName)
		if hasXxHash {
			isHashVerified, err = CheckChunkXxh64HashAsync(
				chunk, asset.AssetName, outStream, hash, true)
		} else {
			// Fall back to MD5 hash
			isHashVerified, err = CheckChunkMd5HashAsync(
				chunk, outStream, true)
		}

		if err != nil {
			return fmt.Errorf("hash verification error: %w", err)
		}

		if !isHashVerified {
			// Report corrupt data and retry
			if writeInfoDelegate != nil {
				writeInfoDelegate(-chunk.ChunkSizeDecompressed)
			}
			if downloadInfoDelegate != nil {
				downloadInfoDelegate(-chunk.ChunkSizeDecompressed, 0)
			}

			PushLogWarning(nil, fmt.Sprintf(
				"Data corruption detected for chunk: %s. Retrying download...",
				chunk.ChunkName))

			continue
		}

		// Download successful
		PushLogDebug(nil, fmt.Sprintf(
			"[%d/%d] Successfully downloaded and verified chunk: %s",
			atomic.LoadInt32(currentChunksDownloadPos),
			countChunksDownload, chunk.ChunkName))

		return nil
	}
}
