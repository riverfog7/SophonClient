package internal

// SophonChunk represents a chunk with metadata
// Default value for ChunkOldOffset is -1 to indicate no reference to old file offset
type SophonChunk struct {
	ChunkName             string
	ChunkHashDecompressed []byte
	ChunkOldOffset        int64
	ChunkOffset           int64
	ChunkSize             int64
	ChunkSizeDecompressed int64
}

// NewSophonChunk creates a new SophonChunk with default values
func NewSophonChunk() SophonChunk {
	return SophonChunk{
		ChunkOldOffset: -1,
	}
}
