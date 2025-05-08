package internal

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"runtime"
)

// SourceStreamType indicates where the data is being sourced from
type SourceStreamType int

const (
	Internet SourceStreamType = iota
	CachedLocal
	OldReference
)

// WriteToStream downloads and writes chunks sequentially to the provided stream
// This method handles downloading the asset's chunks one by one and writing them to the output stream.
// Each chunk is verified using its MD5 hash to ensure data integrity.
func (asset *SophonAsset) WriteToStream(
	ctx context.Context,
	client *http.Client,
	outStream io.ReadWriteSeeker,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadCompleteDelegate DelegateDownloadAssetComplete,
) error {
	// Validate asset and stream state
	if err := EnsureOrThrowChunksState(asset); err != nil {
		return err
	}
	if err := EnsureOrThrowStreamState(asset, outStream); err != nil {
		return err
	}

	// Ensure output stream is properly sized
	currentSize, err := outStream.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to determine stream size: %w", err)
	}
	if currentSize > asset.AssetSize {
		if err := setStreamLength(outStream, asset.AssetSize); err != nil {
			return err
		}
	}

	// Download each chunk sequentially
	for _, chunk := range asset.Chunks {
		if err := asset.performWriteStreamThread(
			ctx, client, nil, Internet, outStream, chunk,
			writeInfoDelegate, downloadInfoDelegate, asset.DownloadSpeedLimiter,
		); err != nil {
			return err
		}
	}

	// Log completion and invoke callback
	PushLogInfo(nil, fmt.Sprintf("Asset: %s | (Hash: %s -> %d bytes) has been completely downloaded!",
		asset.AssetName, asset.AssetHash, asset.AssetSize))

	if downloadCompleteDelegate != nil {
		downloadCompleteDelegate(asset)
	}

	return nil
}

// WriteToStreamParallel downloads and writes chunks in parallel to individual streams
// This method downloads multiple chunks concurrently, with each chunk writing to its own stream
// provided by the streamFactory function. This is more efficient for large downloads.
func (asset *SophonAsset) WriteToStreamParallel(
	ctx context.Context,
	client *http.Client,
	streamFactory func() io.ReadWriteSeeker,
	maxConcurrency int,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadCompleteDelegate DelegateDownloadAssetComplete,
) error {
	// Validate asset state
	if err := EnsureOrThrowChunksState(asset); err != nil {
		return err
	}

	// Initialize a stream once to validate and set up the file
	initStream := streamFactory()
	if err := EnsureOrThrowStreamState(asset, initStream); err != nil {
		if closer, ok := initStream.(io.Closer); ok {
			closer.Close()
		}
		return err
	}

	// Set proper file size
	currentSize, err := initStream.Seek(0, io.SeekEnd)
	if err != nil {
		if closer, ok := initStream.(io.Closer); ok {
			closer.Close()
		}
		return fmt.Errorf("failed to determine stream size: %w", err)
	}
	if currentSize > asset.AssetSize {
		if err := setStreamLength(initStream, asset.AssetSize); err != nil {
			if closer, ok := initStream.(io.Closer); ok {
				closer.Close()
			}
			return err
		}
	}

	// Close the init stream
	if closer, ok := initStream.(io.Closer); ok {
		closer.Close()
	}

	// Set default concurrency if not provided
	if maxConcurrency <= 0 {
		maxConcurrency = min(8, runtime.NumCPU())
	}

	// Set up wait group and semaphore for concurrency control
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)
	var firstErr error
	var errMu sync.Mutex

	// Process each chunk in parallel
	for _, chunk := range asset.Chunks {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore slot

		go func(c *SophonChunk) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			// Create a new stream for this chunk
			stream := streamFactory()
			defer func() {
				if closer, ok := stream.(io.Closer); ok {
					closer.Close()
				}
			}()

			// Process the chunk
			err := asset.performWriteStreamThread(
				ctx, client, nil, Internet, stream, c,
				writeInfoDelegate, downloadInfoDelegate, asset.DownloadSpeedLimiter,
			)

			// Store first error encountered
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(chunk)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Return any error that occurred, or invoke completion callback
	if firstErr != nil {
		return firstErr
	}

	PushLogInfo(nil, fmt.Sprintf("Asset: %s | (Hash: %s -> %d bytes) has been completely downloaded!",
		asset.AssetName, asset.AssetHash, asset.AssetSize))

	if downloadCompleteDelegate != nil {
		downloadCompleteDelegate(asset)
	}

	return nil
}

// performWriteStreamThread handles downloading and writing a single chunk
// This method checks if a chunk needs to be downloaded by verifying existing data,
// and if needed, calls innerWriteStreamTo to perform the actual download.
func (asset *SophonAsset) performWriteStreamThread(
	ctx context.Context,
	client *http.Client,
	sourceStream io.ReadSeeker,
	sourceType SourceStreamType,
	outStream io.ReadWriteSeeker,
	chunk *SophonChunk,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
) error {
	// Calculate total size including offset
	totalSizeFromOffset := chunk.ChunkOffset + chunk.ChunkSizeDecompressed

	// Check if we can skip this chunk (if it's already downloaded and verified)
	currentSize, err := outStream.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	isSkipChunk := currentSize >= totalSizeFromOffset

	if isSkipChunk {
		// Verify the chunk hash
		_, err := outStream.Seek(chunk.ChunkOffset, io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek to chunk position: %w", err)
		}

		isMatch, err := CheckChunkMd5HashAsync(chunk, outStream, false)
		if err != nil {
			return err
		}
		isSkipChunk = isMatch
	}

	if isSkipChunk {
		PushLogDebug(nil, fmt.Sprintf("Skipping chunk 0x%x -> L: 0x%x for: %s",
			chunk.ChunkOffset, chunk.ChunkSizeDecompressed, asset.AssetName))

		// Report progress for skipped chunk
		if writeInfoDelegate != nil {
			writeInfoDelegate(chunk.ChunkSizeDecompressed)
		}
		if downloadInfoDelegate != nil {
			fromNetwork := int64(0)
			if chunk.ChunkOldOffset == -1 {
				fromNetwork = chunk.ChunkSizeDecompressed
			}
			downloadInfoDelegate(fromNetwork, 0)
		}
		return nil
	}

	// If we can't skip, download the chunk
	return asset.innerWriteStreamTo(
		ctx, client, sourceStream, sourceType, outStream, chunk,
		writeInfoDelegate, downloadInfoDelegate, downloadSpeedLimiter,
	)
}

// innerWriteStreamTo performs the actual download of a chunk
// This is the core method that handles downloading data from various sources,
// applying throttling, and ensuring data integrity through hash verification.
func (asset *SophonAsset) innerWriteStreamTo(
	ctx context.Context,
	client *http.Client,
	sourceStream io.ReadSeeker,
	sourceType SourceStreamType,
	outStream io.ReadWriteSeeker,
	chunk *SophonChunk,
	writeInfoDelegate DelegateWriteStreamInfo,
	downloadInfoDelegate DelegateWriteDownloadInfo,
	downloadSpeedLimiter *SophonDownloadSpeedLimiter,
) error {
	// Validate source stream for non-Internet sources
	if sourceType != Internet && sourceStream == nil {
		return errors.New("source stream cannot be null under OldReference or CachedLocal mode")
	}

	if sourceType == OldReference && chunk.ChunkOldOffset < 0 {
		return errors.New("OldReference cannot be used if chunk does not have chunk old offset reference")
	}

	// Constants and retry setup
	const retryCount = DefaultRetryAttempt
	currentRetry := 0
	currentWriteOffset := int64(0)

	// Speed limiting variables
	var thisInstanceDownloadLimitBase int64 = -1
	if downloadSpeedLimiter != nil && downloadSpeedLimiter.InitialRequestedSpeed != nil {
		thisInstanceDownloadLimitBase = *downloadSpeedLimiter.InitialRequestedSpeed
	}

	written := int64(0)
	startTime := time.Now()
	var maximumBytesPerSecond float64
	var bitPerUnit float64

	// Calculate bandwidth based on current downloads
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

	// Register speed limiter handlers if available
	speedChangedHandler := func(sender interface{}, newSpeed int64) {
		thisInstanceDownloadLimitBase = newSpeed
		if newSpeed == 0 {
			thisInstanceDownloadLimitBase = -1
		}
		calculateBps()
	}

	countChangedHandler := func(sender interface{}, count int) {
		calculateBps()
	}

	if downloadSpeedLimiter != nil {
		downloadSpeedLimiter.DownloadSpeedChangedEvent = speedChangedHandler
		downloadSpeedLimiter.CurrentChunkProcessingChangedEvent = countChangedHandler
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
		var currentSourceStream io.Reader
		var resp *http.Response
		var zReader *zstd.Decoder

		// Set up MD5 for verification
		hash := md5.New()

		// Set output stream position
		if _, err := outStream.Seek(chunk.ChunkOffset, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek output stream: %w", err)
		}

		// Get source stream based on type
		currentSourceType := sourceType
		PushLogDebug(nil, fmt.Sprintf("Init. by offset: 0x%x -> L: 0x%x for chunk: %s",
			chunk.ChunkOffset, chunk.ChunkSizeDecompressed, chunk.ChunkName))

		// Clean up function for resources
		cleanup := func() {
			if resp != nil {
				resp.Body.Close()
				resp = nil
			}
			if zReader != nil {
				zReader.Close()
				zReader = nil
			}
		}

		// Set up source stream based on type
		var err error
		switch currentSourceType {
		case Internet:
			if downloadSpeedLimiter != nil {
				downloadSpeedLimiter.IncrementChunkProcessedCount()
			}

			resp, err = GetChunkAndIfAltAsync(client, chunk.ChunkName, asset.SophonChunksInfo, asset.SophonChunksInfoAlt)
			if err != nil {
				cleanup()

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

				return fmt.Errorf("failed to download chunk after %d attempts: %w", retryCount, err)
			}

			currentSourceStream = resp.Body

			// Apply zstd decompression if needed
			if asset.SophonChunksInfo.IsUseCompression {
				zReader, err = zstd.NewReader(resp.Body)
				if err != nil {
					cleanup()
					return fmt.Errorf("failed to create zstd reader: %w", err)
				}
				currentSourceStream = zReader
			}

		case CachedLocal:
			if asset.SophonChunksInfo.IsUseCompression {
				zReader, err = zstd.NewReader(sourceStream)
				if err != nil {
					cleanup()
					return fmt.Errorf("failed to create zstd reader: %w", err)
				}
				currentSourceStream = zReader
			} else {
				currentSourceStream = sourceStream
			}

		case OldReference:
			if _, err := sourceStream.Seek(chunk.ChunkOldOffset, io.SeekStart); err != nil {
				cleanup()
				return fmt.Errorf("failed to seek source stream: %w", err)
			}
			currentSourceStream = sourceStream
		}

		PushLogDebug(nil, fmt.Sprintf("[Complete init.] by offset: 0x%x -> L: 0x%x for chunk: %s",
			chunk.ChunkOffset, chunk.ChunkSizeDecompressed, chunk.ChunkName))

		// Download and write loop
		remain := chunk.ChunkSizeDecompressed
		for remain > 0 {
			select {
			case <-ctx.Done():
				cleanup()
				return ctx.Err()
			default:
			}

			toRead := min(int(remain), len(buffer))
			read, err := io.ReadFull(currentSourceStream, buffer[:toRead])
			if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				cleanup()

				if currentRetry < retryCount {
					// Report negative progress for retry
					if writeInfoDelegate != nil {
						writeInfoDelegate(-currentWriteOffset)
					}
					if currentSourceType != OldReference && downloadInfoDelegate != nil {
						downloadInfoDelegate(-currentWriteOffset, 0)
					}

					currentWriteOffset = 0
					currentRetry++
					sourceType = Internet // Fall back to Internet source on failure

					PushLogWarning(nil, fmt.Sprintf(
						"Error reading chunk: %s | Retry %d/%d: %v",
						chunk.ChunkName, currentRetry, retryCount, err))

					// Wait before retry
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Second):
					}
					break
				}

				return fmt.Errorf("failed to read chunk after %d attempts: %w", retryCount, err)
			}

			if read == 0 {
				if remain > 0 {
					cleanup()
					return fmt.Errorf("chunk has remained data while read is 0: %d bytes left", remain)
				}
				break
			}

			// Write to output and update hash
			_, err = outStream.Write(buffer[:read])
			if err != nil {
				cleanup()
				return fmt.Errorf("failed to write to output stream: %w", err)
			}

			currentWriteOffset += int64(read)
			remain -= int64(read)
			hash.Write(buffer[:read])

			// Report progress
			if writeInfoDelegate != nil {
				writeInfoDelegate(int64(read))
			}

			// Add network activity indicator
			if currentSourceType != OldReference && downloadInfoDelegate != nil {
				networkBytes := int64(0)
				if currentSourceType == Internet {
					networkBytes = int64(read)
					written += int64(read)
				}
				downloadInfoDelegate(int64(read), networkBytes)
			}

			// Reset retry counter as we're making progress
			currentRetry = 0

			// Apply throttling for Internet downloads
			if currentSourceType == Internet {
				if err := throttle(); err != nil {
					cleanup()
					return err
				}
			}
		}

		// Verify hash of downloaded data
		sum := hash.Sum(nil)
		if !bytesEqual(sum, chunk.ChunkHashDecompressed) {
			cleanup()

			// Report corrupted data
			if writeInfoDelegate != nil {
				writeInfoDelegate(-currentWriteOffset)
			}
			if currentSourceType != OldReference && downloadInfoDelegate != nil {
				downloadInfoDelegate(-currentWriteOffset, 0)
			}

			PushLogWarning(nil, fmt.Sprintf(
				"Source data from type: %v is corrupted. Retrying for chunk: %s",
				currentSourceType, chunk.ChunkName))

			// Fall back to Internet source on corruption
			sourceType = Internet
			currentWriteOffset = 0
			continue
		}

		// Download successful
		cleanup()
		PushLogDebug(nil, fmt.Sprintf("Download completed! Chunk: %s | 0x%x -> L: 0x%x for: %s",
			chunk.ChunkName, chunk.ChunkOffset, chunk.ChunkSizeDecompressed, asset.AssetName))

		return nil
	}
}

// Helper functions

// setStreamLength sets the length of a stream
func setStreamLength(stream io.ReadWriteSeeker, length int64) error {
	// Since Go's io doesn't have a SetLength method, we need to implement it
	// by seeking to the desired position and writing a single byte
	_, err := stream.Seek(length-1, io.SeekStart)
	if err != nil {
		return err
	}

	_, err = stream.Write([]byte{0})
	return err
}

// bytesEqual compares two byte slices
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
