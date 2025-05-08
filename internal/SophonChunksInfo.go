package internal

import "hash/fnv"

// SophonChunksInfo represents information about Sophon chunks
// It includes metadata about chunk URLs, counts, sizes, and compression usage
type SophonChunksInfo struct {
	ChunksBaseUrl       string
	ChunksCount         int
	FilesCount          int
	TotalSize           int64
	TotalCompressedSize int64
	IsUseCompression    bool
}

// Equals checks if two SophonChunksInfo instances are equal
func (s *SophonChunksInfo) Equals(other *SophonChunksInfo) bool {
	if other == nil {
		return false
	}
	return s.ChunksBaseUrl == other.ChunksBaseUrl &&
		s.ChunksCount == other.ChunksCount &&
		s.FilesCount == other.FilesCount &&
		s.TotalSize == other.TotalSize &&
		s.TotalCompressedSize == other.TotalCompressedSize &&
		s.IsUseCompression == other.IsUseCompression
}

// GetHashCode provides a hash code for the SophonChunksInfo
// This is similar to C#'s GetHashCode method
func (s *SophonChunksInfo) GetHashCode() uint32 {
	h := fnv.New32a()
	h.Write([]byte(s.ChunksBaseUrl))
	h.Write([]byte{byte(s.ChunksCount), byte(s.ChunksCount >> 8), byte(s.ChunksCount >> 16), byte(s.ChunksCount >> 24)})
	h.Write([]byte{byte(s.FilesCount), byte(s.FilesCount >> 8), byte(s.FilesCount >> 16), byte(s.FilesCount >> 24)})
	h.Write([]byte{byte(s.TotalSize), byte(s.TotalSize >> 8), byte(s.TotalSize >> 16), byte(s.TotalSize >> 24)})
	h.Write([]byte{byte(s.TotalCompressedSize), byte(s.TotalCompressedSize >> 8), byte(s.TotalCompressedSize >> 16), byte(s.TotalCompressedSize >> 24)})
	if s.IsUseCompression {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	return h.Sum32()
}

// CopyWithNewBaseUrl returns a copy of SophonChunksInfo with a new base URL
func (s *SophonChunksInfo) CopyWithNewBaseUrl(newBaseUrl string) *SophonChunksInfo {
	return &SophonChunksInfo{
		ChunksBaseUrl:       newBaseUrl,
		ChunksCount:         s.ChunksCount,
		FilesCount:          s.FilesCount,
		TotalSize:           s.TotalSize,
		TotalCompressedSize: s.TotalCompressedSize,
		IsUseCompression:    s.IsUseCompression,
	}
}

// CreateChunksInfo creates a new SophonChunksInfo instance
// Parameters:
//   - chunksBaseUrl: The base URL for the chunks. From API section: chunk_download -> url_prefix
//   - chunksCount: The count of chunks to be downloaded. From API section: stats -> chunk_count
//   - filesCount: The count of files to be downloaded. From API section: stats -> file_count
//   - isUseCompression: Determine the use of compression. From API section: chunk_download -> compression
//   - totalSize: Total decompressed size of files. From API section: stats -> uncompressed_size
//   - totalCompressedSize: Total compressed size of files. From API section: stats -> compressed_size
//
// Returns:
//   - A new SophonChunksInfo instance
func CreateChunksInfo(chunksBaseUrl string, chunksCount int, filesCount int, isUseCompression bool, totalSize int64, totalCompressedSize int64) *SophonChunksInfo {
	return &SophonChunksInfo{
		ChunksBaseUrl:       chunksBaseUrl,
		ChunksCount:         chunksCount,
		FilesCount:          filesCount,
		IsUseCompression:    isUseCompression,
		TotalSize:           totalSize,
		TotalCompressedSize: totalCompressedSize,
	}
}
