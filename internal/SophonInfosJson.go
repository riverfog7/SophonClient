package internal

// SophonManifestBuildBranch inherits from SophonBranch
type SophonManifestBuildBranch struct {
	SophonBranch
}

// SophonBranch inherits from SophonManifestReturnedResponse
// Note: This struct is marked as obsolete in the original C# code
type SophonBranch struct {
	Data *SophonManifestBuildData `json:"data"`
	SophonManifestReturnedResponse
}

// SophonManifestBuildData inherits from SophonData
type SophonManifestBuildData struct {
	SophonData
}

// SophonData inherits from SophonTaggedResponse
// Note: This struct is marked as obsolete in the original C# code
type SophonData struct {
	ManifestIdentityList []SophonManifestBuildIdentity `json:"manifests"`
	SophonTaggedResponse
}

// SophonManifestBuildIdentity inherits from SophonManifestIdentity
type SophonManifestBuildIdentity struct {
	SophonManifestIdentity
	ChunkInfo             SophonManifestChunkInfo `json:"stats"`
	ChunksUrlInfo         SophonManifestUrlInfo   `json:"chunk_download"`
	DeduplicatedChunkInfo SophonManifestChunkInfo `json:"deduplicated_stats"`
}

// SophonManifestPatchBranch inherits from SophonManifestReturnedResponse
type SophonManifestPatchBranch struct {
	Data *SophonManifestPatchData `json:"data"`
	SophonManifestReturnedResponse
}

// SophonManifestPatchData inherits from SophonTaggedResponse
type SophonManifestPatchData struct {
	PatchId              string                        `json:"patch_id"`
	ManifestIdentityList []SophonManifestPatchIdentity `json:"manifests"`
	SophonTaggedResponse
}

// SophonManifestPatchIdentity inherits from SophonManifestIdentity
type SophonManifestPatchIdentity struct {
	SophonManifestIdentity
	DiffUrlInfo    SophonManifestUrlInfo              `json:"diff_download"`
	DiffTaggedInfo map[string]SophonManifestChunkInfo `json:"stats"`
}

// SophonManifestReturnedResponse base structure
type SophonManifestReturnedResponse struct {
	ReturnCode    int    `json:"retcode"`
	ReturnMessage string `json:"message"`
}

// SophonTaggedResponse base structure
type SophonTaggedResponse struct {
	BuildId string `json:"build_id"`
	TagName string `json:"tag"`
}

// SophonManifestIdentity base structure
// Note: This struct is marked as obsolete in the original C# code
type SophonManifestIdentity struct {
	CategoryId       int                    `json:"category_id,string"`
	CategoryName     string                 `json:"category_name"`
	MatchingField    string                 `json:"matching_field"`
	ManifestFileInfo SophonManifestFileInfo `json:"manifest"`
	ManifestUrlInfo  SophonManifestUrlInfo  `json:"manifest_download"`
}

// SophonManifestFileInfo structure
type SophonManifestFileInfo struct {
	FileName         string `json:"id"`
	Checksum         string `json:"checksum"`
	CompressedSize   int64  `json:"compressed_size,string"`
	UncompressedSize int64  `json:"uncompressed_size,string"`
}

// SophonManifestUrlInfo structure
type SophonManifestUrlInfo struct {
	EncryptionPassword string `json:"password"`
	UrlPrefix          string `json:"url_prefix"`
	UrlSuffix          string `json:"url_suffix"`
	IsEncrypted        bool   `json:"encryption"`
	IsCompressed       bool   `json:"compression"`
}

// SophonManifestChunkInfo structure
type SophonManifestChunkInfo struct {
	CompressedSize   int64 `json:"compressed_size,string"`
	UncompressedSize int64 `json:"uncompressed_size,string"`
	FileCount        int   `json:"file_count,string"`
	ChunkCount       int   `json:"chunk_count,string"`
}
