package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/riverfog7/SophonClient/internal"
	"io"
	"net/http"
	"os"
)

// SophonInformation represents collected manifest information
type SophonInformation struct {
	CategoryID            int                              `json:"categoryId"`
	CategoryName          string                           `json:"categoryName"`
	MatchingField         string                           `json:"matchingField"`
	ManifestFileInfo      internal.SophonManifestFileInfo  `json:"manifestFileInfo"`
	ManifestUrlInfo       internal.SophonManifestUrlInfo   `json:"manifestUrlInfo"`
	ChunksUrlInfo         internal.SophonManifestUrlInfo   `json:"chunksUrlInfo"`
	ChunkInfo             internal.SophonManifestChunkInfo `json:"chunkInfo"`
	DeduplicatedChunkInfo internal.SophonManifestChunkInfo `json:"deduplicatedChunkInfo"`
	Assets                []internal.SophonAsset           `json:"assets"`
}

// runManifestInfo contains the logic from the original main function
func runManifestInfo(branchInfoURL, outputPath string) {
	writeToConsole := outputPath == "-"

	// Create HTTP client
	client := &http.Client{}

	// Fetch Sophon branch information
	branch, err := getSophonBranchInfo(client, branchInfoURL)
	if err != nil {
		fmt.Printf("Error fetching branch info: %v\n", err)
		os.Exit(1)
	}

	// Check response status
	if branch.ReturnCode != 0 || branch.Data == nil {
		fmt.Printf("Error: %s (Code: %d)\n", branch.ReturnMessage, branch.ReturnCode)
		os.Exit(1)
	}

	// Process manifest identities
	var sophonInfos []SophonInformation
	for _, manifestIdentity := range branch.Data.ManifestIdentityList {
		infoPair, err := createChunkManifestInfoPair(client, branchInfoURL, manifestIdentity.MatchingField)
		if err != nil {
			fmt.Printf("Error creating info pair: %v\n", err)
			os.Exit(1)
		}

		assets, err := enumerateAssets(client, infoPair)
		if err != nil {
			fmt.Printf("Error enumerating assets: %v\n", err)
			os.Exit(1)
		}

		sophonInfo := SophonInformation{
			CategoryID:            manifestIdentity.CategoryId,
			CategoryName:          manifestIdentity.CategoryName,
			MatchingField:         manifestIdentity.MatchingField,
			ManifestFileInfo:      manifestIdentity.ManifestFileInfo,
			ManifestUrlInfo:       manifestIdentity.ManifestUrlInfo,
			ChunksUrlInfo:         manifestIdentity.ChunksUrlInfo,
			ChunkInfo:             manifestIdentity.ChunkInfo,
			DeduplicatedChunkInfo: manifestIdentity.DeduplicatedChunkInfo,
			Assets:                assets,
		}

		sophonInfos = append(sophonInfos, sophonInfo)
	}

	// Write output
	if err := writeOutput(sophonInfos, outputPath, writeToConsole); err != nil {
		fmt.Printf("Error writing output: %v\n", err)
		os.Exit(1)
	}
}

func getSophonBranchInfo(client *http.Client, url string) (*internal.SophonManifestBuildBranch, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var branch internal.SophonManifestBuildBranch
	if err := json.NewDecoder(resp.Body).Decode(&branch); err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %w", err)
	}

	return &branch, nil
}

func createChunkManifestInfoPair(client *http.Client, url, matchingField string) (*internal.SophonChunkManifestInfoPair, error) {
	httpClient := internal.NewSophonHTTPClient(client)
	return httpClient.CreateSophonChunkManifestInfoPair(context.Background(), url, matchingField)
}

func enumerateAssets(client *http.Client, infoPair *internal.SophonChunkManifestInfoPair) ([]internal.SophonAsset, error) {
	res, err := internal.Enumerate(context.Background(), client, infoPair, nil)
	lst := make([]internal.SophonAsset, 0)
	if err == nil {
		for asset := range res {
			lst = append(lst, *asset)
		}
	}
	return lst, err
}

func writeOutput(data []SophonInformation, path string, useStdout bool) error {
	var output io.Writer = os.Stdout
	if !useStdout {
		file, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer file.Close()
		output = file
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}
