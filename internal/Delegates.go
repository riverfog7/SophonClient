package internal

// DelegateWriteStreamInfo is a callback function type to report the number of bytes written per cycle to disk
type DelegateWriteStreamInfo func(writeBytes int64)

// DelegateWriteDownloadInfo is a callback function type to report download and disk write progress
type DelegateWriteDownloadInfo func(downloadedBytes, diskWriteBytes int64)

// DelegateDownloadAssetComplete is a callback function type to report when an asset download is complete
type DelegateDownloadAssetComplete func(asset *SophonAsset)
