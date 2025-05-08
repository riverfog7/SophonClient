package internal

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
)

// DownloadPatch downloads a patch file to the specified output directory
// This method handles downloading patches for CopyOver and Patch operations.
// It skips downloading for Remove and DownloadOver operations which handle their
// own resources differently. It includes hash verification to ensure patch integrity.
func (asset *SophonPatchAsset) DownloadPatch(
	ctx context.Context,
	client *http.Client,
	patchOutputDir string,
	forceVerification bool,
	downloadReadDelegate func(int64),
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
) error {
	// Ignore SophonPatchMethod.Remove and SophonPatchMethod.DownloadOver assets
	if asset.PatchMethod == Remove || asset.PatchMethod == DownloadOver {
		return nil
	}

	// Setup patch file paths
	patchNameHashed := asset.PatchNameSource
	patchFilePathHashed := filepath.Join(patchOutputDir, patchNameHashed)

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(patchFilePathHashed), 0755); err != nil {
		return fmt.Errorf("failed to create patch directory: %w", err)
	}

	// Make file writable if it exists
	UnassignReadOnlyFromFileInfo(patchFilePathHashed)

	// Get the patch hash
	var patchHash []byte
	var err error

	// Try to get xxHash first, fall back to MD5
	patchHash, ok := TryGetChunkXxh64Hash(asset.PatchNameSource)
	if !ok {
		patchHash, err = HexToBytes(asset.PatchHash)
		if err != nil {
			return fmt.Errorf("failed to decode patch hash: %w", err)
		}
	}

	// Create chunk representation of the patch
	patchAsChunk := &SophonChunk{
		ChunkHashDecompressed: patchHash,
		ChunkName:             asset.PatchNameSource,
		ChunkOffset:           0,
		ChunkOldOffset:        -1, // Default value as in C# constructor
		ChunkSize:             asset.PatchSize,
		ChunkSizeDecompressed: asset.PatchSize,
	}

	// Open or create the file
	file, err := os.OpenFile(patchFilePathHashed, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open patch file: %w", err)
	}
	defer file.Close()

	// Check if we need to download
	fileInfo, err := file.Stat()
	isPatchUnmatched := err != nil || fileInfo.Size() != asset.PatchSize

	// Verify hash if requested
	if forceVerification && !isPatchUnmatched {
		file.Seek(0, io.SeekStart)
		var isHashVerified bool

		// Use appropriate hash verification based on hash length (xxHash vs MD5)
		if len(patchHash) > 8 {
			// Use MD5
			isHashVerified, err = CheckChunkMd5HashAsync(patchAsChunk, file, true)
		} else {
			// Use xxHash
			isHashVerified, err = CheckChunkXxh64HashAsync(patchAsChunk, asset.PatchNameSource, file, patchHash, true)
		}

		if err != nil {
			PushLogWarning(asset, fmt.Sprintf("Failed to verify patch hash: %v", err))
			isPatchUnmatched = true
		} else if !isHashVerified {
			isPatchUnmatched = true
			// Reset file for download
			file.Seek(0, io.SeekStart)
			if err := file.Truncate(0); err != nil {
				return fmt.Errorf("failed to truncate file: %w", err)
			}
		}
	}

	// If patch is already downloaded and verified, just report progress and return
	if !isPatchUnmatched {
		PushLogDebug(asset, fmt.Sprintf("Skipping patch %s for: %s", asset.PatchNameSource, asset.TargetFilePath))
		if downloadReadDelegate != nil {
			downloadReadDelegate(asset.PatchSize)
		}
		return nil
	}

	// Reset file position for download
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to beginning of file: %w", err)
	}

	// Download the patch
	return asset.innerWriteChunkCopy(
		ctx,
		client,
		file,
		patchAsChunk,
		asset.PatchInfo,
		asset.PatchInfo,
		nil, // No write info delegate
		func(bytesRead, bytesWritten int64) {
			if downloadReadDelegate != nil {
				downloadReadDelegate(bytesWritten)
			}
		},
		downloadSpeedLimiter,
	)
}

// innerWriteChunkCopy performs the actual download of a chunk/patch
// This method handles downloading from the network, throttling download speed,
// handling retries on failures, and verifying downloaded content with hash checks.
func (asset *SophonPatchAsset) innerWriteChunkCopy(
	ctx context.Context,
	client *http.Client,
	outStream io.ReadWriteSeeker,
	chunk *SophonChunk,
	currentSophonChunkInfo *SophonChunksInfo,
	altSophonChunkInfo *SophonChunksInfo,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
) error {
	const retryCount = DefaultRetryAttempt

	// Counters and tracking variables
	currentRetry := 0
	currentWriteOffset := int64(0)
	written := int64(0)
	startTime := time.Now()

	// Speed limiting setup
	var thisInstanceDownloadLimitBase int64 = -1
	if downloadSpeedLimiter != nil && downloadSpeedLimiter.InitialRequestedSpeed != nil {
		thisInstanceDownloadLimitBase = *downloadSpeedLimiter.InitialRequestedSpeed
	}

	var maximumBytesPerSecond float64
	var bitPerUnit float64

	// Calculate bandwidth based on available cores and speed limit
	calculateBps := func() {
		if thisInstanceDownloadLimitBase <= 0 {
			thisInstanceDownloadLimitBase = -1
			maximumBytesPerSecond = -1
			return
		}

		// Set minimum speed limit (64KB)
		thisInstanceDownloadLimitBase = max(int64(64<<10), thisInstanceDownloadLimitBase)

		// Get concurrent download count
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

	// Register with download speed limiter
	if downloadSpeedLimiter != nil {
		downloadSpeedLimiter.DownloadSpeedChangedEvent = func(sender interface{}, newSpeed int64) {
			thisInstanceDownloadLimitBase = newSpeed
			if newSpeed == 0 {
				thisInstanceDownloadLimitBase = -1
			}
			calculateBps()
		}

		downloadSpeedLimiter.CurrentChunkProcessingChangedEvent = func(sender interface{}, count int) {
			calculateBps()
		}

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

	// Buffer for downloading
	buffer := make([]byte, asset.BufferSize)

	// Main download loop with retries
	for {
		// Initialize resources that need cleanup
		var resp *http.Response
		var zReader *zstd.Decoder

		// Reset file position
		if _, err := outStream.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek output stream: %w", err)
		}

		PushLogDebug(asset, fmt.Sprintf("Init download for chunk: %s", chunk.ChunkName))

		// Function to cleanup resources
		cleanup := func() {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			if zReader != nil {
				zReader.Close()
			}
		}

		// Try to download the chunk
		var err error
		resp, err = GetChunkAndIfAltAsync(client, chunk.ChunkName, currentSophonChunkInfo, altSophonChunkInfo)
		if err != nil {
			cleanup()

			// If we have retries left, try again
			if currentRetry < retryCount {
				if writeInfoDelegate != nil {
					writeInfoDelegate(-currentWriteOffset)
				}
				if downloadInfoDelegate != nil {
					downloadInfoDelegate(-currentWriteOffset, 0)
				}

				currentWriteOffset = 0
				currentRetry++

				PushLogWarning(asset, fmt.Sprintf(
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

			return fmt.Errorf("failed to download chunk after %d attempts: %w", retryCount, err)
		}

		// Set up the source stream, handling compression if needed
		var sourceStream io.Reader = resp.Body
		if currentSophonChunkInfo.IsUseCompression != 0 {
			zReader, err = zstd.NewReader(resp.Body)
			if err != nil {
				cleanup()
				return fmt.Errorf("failed to create zstd reader: %w", err)
			}
			sourceStream = zReader
		}

		PushLogDebug(asset, fmt.Sprintf("Download initialized for chunk: %s", chunk.ChunkName))

		// Read and write loop
		hasError := false
		md5Hash := md5.New()
		for {
			select {
			case <-ctx.Done():
				cleanup()
				return ctx.Err()
			default:
			}

			// Read from source
			read, err := sourceStream.Read(buffer)
			if err != nil && err != io.EOF {
				hasError = true
				break
			}

			if read == 0 {
				break
			}

			// Write to output and update hash
			_, err = outStream.Write(buffer[:read])
			if err != nil {
				cleanup()
				return fmt.Errorf("failed to write to output stream: %w", err)
			}

			currentWriteOffset += int64(read)
			written += int64(read)
			md5Hash.Write(buffer[:read])

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
				cleanup()
				return err
			}
		}

		// If there was an error during download, retry if possible
		if hasError {
			cleanup()

			if currentRetry < retryCount {
				if writeInfoDelegate != nil {
					writeInfoDelegate(-currentWriteOffset)
				}
				if downloadInfoDelegate != nil {
					downloadInfoDelegate(-currentWriteOffset, 0)
				}

				currentWriteOffset = 0
				currentRetry++

				PushLogWarning(asset, fmt.Sprintf(
					"Error during download for chunk: %s | Retry %d/%d",
					chunk.ChunkName, currentRetry, retryCount))

				// Wait before retry
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
				continue
			}

			return errors.New("failed to download chunk - read operation failed")
		}

		// Verify downloaded data
		outStream.Seek(0, io.SeekStart)
		var isHashVerified bool

		// Use appropriate hash verification based on hash length
		if hash, ok := TryGetChunkXxh64Hash(chunk.ChunkName); ok {
			isHashVerified, err = CheckChunkXxh64HashAsync(
				chunk, asset.TargetFilePath, outStream, hash, true)
		} else {
			// Use MD5 hash
			isHashVerified, err = CheckChunkMd5HashAsync(
				chunk, outStream, true)
		}

		if err != nil {
			cleanup()
			return fmt.Errorf("hash verification error: %w", err)
		}

		if !isHashVerified {
			cleanup()

			// Report corrupt data and retry
			if writeInfoDelegate != nil {
				writeInfoDelegate(-chunk.ChunkSizeDecompressed)
			}
			if downloadInfoDelegate != nil {
				downloadInfoDelegate(-chunk.ChunkSizeDecompressed, 0)
			}

			PushLogWarning(asset, fmt.Sprintf(
				"Data corruption detected for chunk: %s. Retrying download...",
				chunk.ChunkName))

			currentWriteOffset = 0
			continue
		}

		// Download successful
		cleanup()
		PushLogDebug(asset, fmt.Sprintf(
			"Successfully downloaded and verified chunk: %s",
			chunk.ChunkName))

		return nil
	}
}

// Helper functions

// max returns the larger of two values
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
