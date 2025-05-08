package internal

// SophonPatchMethod represents the different methods for applying patches
type SophonPatchMethod int

const (
	CopyOver SophonPatchMethod = iota
	DownloadOver
	Patch
	Remove
)

// SophonPatchAsset contains information about a patch asset
type SophonPatchAsset struct {
	// Buffer size constant
	BufferSize int // = 256 << 10

	// Patch information
	PatchInfo        *SophonChunksInfo
	PatchMethod      SophonPatchMethod
	PatchNameSource  string
	PatchHash        string
	PatchOffset      int64
	PatchSize        int64
	PatchChunkLength int64

	// Original file information
	OriginalFilePath string
	OriginalFileHash string
	OriginalFileSize int64

	// Target file information
	TargetFilePath                string
	TargetFileDownloadOverBaseUrl string
	TargetFileHash                string
	TargetFileSize                int64

	// Not explicitly defined in properties but used in methods
	DownloadSpeedLimiter *SophonDownloadSpeedLimiter
}
