package internal

type SophonAsset struct {
	_countChunksDownload        int
	_currentChunksDownloadPos   int
	_currentChunksDownloadQueue int

	AssetName            string
	AssetSize            int64
	AssetHash            string
	IsDirectory          bool
	IsHasPatch           bool
	Chunks               []*SophonChunk
	DownloadSpeedLimiter *SophonDownloadSpeedLimiter
	SophonChunksInfo     *SophonChunksInfo
	SophonChunksInfoAlt  *SophonChunksInfo

	BufferSize     int
	ZstdBufferSize int
}
