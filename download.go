package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/riverfog7/SophonClient/internal"
)

var (
	cancelMessage = "[\"C\"] Stop or [\"R\"] Restart"
	totalSize     uint64
	currentRead   atomic.Uint64
	isRetry       bool
	sizeSuffixes  = []string{"B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
)

func usageHelpDownload() int {
	executableName := filepath.Base(os.Args[0])
	fmt.Printf("%s [Sophon Build URL] [Matching field name (usually, you can set \"game\" as the value)] [Download Output Path] [OPTIONAL: Amount of threads to be used (Default: %d)] [OPTIONAL: Amount of max. connection used for Http Client (Default: 128)]\n",
		executableName, runtime.NumCPU())
	return 1
}

func DownloadCommand(URL string, FieldName string, Path string, ThreadCount int, MaxConnections int) int {
	threads := ThreadCount
	maxHttpHandle := MaxConnections
	buildURL := URL
	matchingField := FieldName
	outputDir := Path

	// Setup logger
	internal.LogHandler = func(sender interface{}, log internal.LogStruct) {
		if log.LogLevel != internal.Debug {
			fmt.Printf("[%v] %s\n", log.LogLevel, log.Message)
		}
	}

startDownload:
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelMessage = "[\"C\"] Stop or [\"R\"] Restart"

	// Create HTTP client with connection limits
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: maxHttpHandle,
			MaxConnsPerHost:     maxHttpHandle,
		},
	}

	// Get manifest information
	manifestPair, err := createChunkManifestInfoPair(client, buildURL, matchingField)
	if err != nil {
		fmt.Printf("Error getting manifest: %v\n", err)
		return 1
	}

	sophonChunksInfo := manifestPair.ChunksInfo

	// Enumerate assets
	assetChan, err := internal.Enumerate(ctx, client, manifestPair, nil)
	if err != nil {
		fmt.Printf("Error enumerating assets: %v\n", err)
		return 1
	}

	var sophonAssets []*internal.SophonAsset
	for asset := range assetChan {
		if !asset.IsDirectory {
			sophonAssets = append(sophonAssets, asset)
		}
	}

	// Setup key monitoring
	go appExitKeyTrigger(ctx, cancel)

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Create chunk output directory
	chunkOutPath := filepath.Join(outputDir, "chunk_collapse")
	if err := os.MkdirAll(chunkOutPath, 0755); err != nil {
		fmt.Printf("Error creating directory: %v\n", err)
	}

	// Delete temp files
	files, _ := filepath.Glob(filepath.Join(outputDir, "*_tempUpdate"))
	for _, f := range files {
		os.Remove(f)
	}

	//totalSizeUnit := summarizeSizeSimple(float64(sophonChunksInfo.TotalSize))
	//totalSizeDiffUnit := "0 B"
	isRetry = false

	// Setup worker pool
	var wg sync.WaitGroup
	sem := make(chan struct{}, threads)
	startTime := time.Now()

	// Start progress reporter
	stopProgress := make(chan struct{})
	go reportProgress(stopProgress, sophonChunksInfo.TotalSize, startTime)

	// Process assets
	for _, asset := range sophonAssets {
		select {
		case <-ctx.Done():
			goto cleanup
		case sem <- struct{}{}:
			wg.Add(1)
			go func(a *internal.SophonAsset) {
				defer func() {
					<-sem
					wg.Done()
				}()
				downloadAsset(ctx, client, a, outputDir, &currentRead)
			}(asset)
		}
	}

cleanup:
	wg.Wait()
	close(stopProgress)
	client.CloseIdleConnections()

	if isRetry {
		currentRead = atomic.Uint64{}
		goto startDownload
	}

	return 0
}

func downloadAsset(ctx context.Context, client *http.Client, asset *internal.SophonAsset, outputDir string, currentRead *atomic.Uint64) {
	outputPath := filepath.Join(outputDir, asset.AssetName)
	os.MkdirAll(filepath.Dir(outputPath), 0755)

	// Create a stream factory function
	streamFactory := func() io.ReadWriteSeeker {
		file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			// Handle error appropriately
			return nil
		}
		return file
	}

	// Download the asset
	err := asset.WriteToStreamParallel(
		ctx,
		client,
		streamFactory,
		runtime.NumCPU(),
		func(read int64) {
			currentRead.Add(uint64(read))
		},
		nil,
		nil,
	)

	if err != nil && ctx.Err() == nil {
		log.Printf("Failed to download %s: %v", asset.AssetName, err)
	} else if ctx.Err() == nil {
		fmt.Printf("Downloaded: %s\n", asset.AssetName)
	}
}

func reportProgress(stop chan struct{}, totalSize int64, startTime time.Time) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			current := currentRead.Load()
			elapsed := time.Since(startTime).Seconds()
			speed := float64(current) / elapsed

			fmt.Printf("\r%s | %s/%s (%s/s)    ",
				cancelMessage,
				summarizeSizeSimple(float64(current)),
				summarizeSizeSimple(float64(totalSize)),
				summarizeSizeSimple(speed),
			)
		case <-stop:
			fmt.Println("\nDownload completed!")
			return
		}
	}
}

func appExitKeyTrigger(ctx context.Context, cancel context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			var b [1]byte
			_, err := os.Stdin.Read(b[:])
			if err != nil {
				continue
			}

			switch b[0] {
			case 'C', 'c':
				cancelMessage = "Cancelling download..."
				cancel()
				return
			case 'R', 'r':
				isRetry = true
				cancelMessage = "Restarting download..."
				cancel()
				return
			}
		}
	}
}

func summarizeSizeSimple(value float64, decimalPlaces ...int) string {
	if value == 0 {
		return "0 B"
	}

	dp := 2
	if len(decimalPlaces) > 0 {
		dp = decimalPlaces[0]
	}

	// Calculate magnitude
	mag := 0
	for value >= 1024 && mag < len(sizeSuffixes)-1 {
		value /= 1024
		mag++
	}

	// Format with specified decimal places
	return fmt.Sprintf("%."+strconv.Itoa(dp)+"f %s", value, sizeSuffixes[mag])
}
