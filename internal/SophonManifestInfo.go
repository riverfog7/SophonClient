package internal

import "strings"

// SophonManifestInfo represents information about a Sophon manifest
type SophonManifestInfo struct {
	ManifestBaseUrl        string
	ManifestId             string
	ManifestChecksumMd5    string
	IsUseCompression       int
	ManifestSize           int64
	ManifestCompressedSize int64
}

// ManifestFileUrl returns the complete URL for the manifest file
func (s *SophonManifestInfo) ManifestFileUrl() string {
	return strings.TrimRight(s.ManifestBaseUrl, "/") + "/" + s.ManifestId
}

// CreateManifestInfo creates a new SophonManifestInfo instance
//
// Parameters:
//   - manifestBaseUrl: The base URL for the manifest. See the API section: manifest_download -> url_prefix
//   - manifestChecksumMd5: The MD5 hash of the manifest. See the API section: manifest -> checksum
//   - manifestId: The file name/id of the manifest. See the API section: manifest -> id
//   - isUseCompression: Determine the use of compression within the manifest. See the API section: manifest_download -> compression
//   - manifestSize: The decompressed size of the manifest file. See the API section: stats -> uncompressed_size
//   - manifestCompressedSize: The compressed size of the manifest file. See the API section: stats -> compressed_size
//
// Returns:
//   - A new SophonManifestInfo instance
func CreateManifestInfo(
	manifestBaseUrl string,
	manifestChecksumMd5 string,
	manifestId string,
	isUseCompression int,
	manifestSize int64,
	manifestCompressedSize int64,
) *SophonManifestInfo {
	return &SophonManifestInfo{
		ManifestBaseUrl:        manifestBaseUrl,
		ManifestChecksumMd5:    manifestChecksumMd5,
		ManifestId:             manifestId,
		IsUseCompression:       isUseCompression,
		ManifestSize:           manifestSize,
		ManifestCompressedSize: manifestCompressedSize,
	}
}
