package database

import "time"

// FileState 代表一个文件在上次同步完成时的快照状态
// 存入数据库时会序列化为 JSON
type FileState struct {
	// 相对路径 (作为数据库的 Key，这里也存一份冗余方便反序列化)
	// 格式示例: "docs/report.pdf" (统一使用 / 作为分隔符)
	RelPath string `json:"rel_path"`

	// 文件大小 (字节)
	// 注意：如果是加密上传，这里建议存储【本地明文大小】
	// 如果存储的是云端密文大小，比对时需要做转换逻辑
	FileSize int64 `json:"file_size"`

	// 修改时间 (Unix Nano)
	ModTime int64 `json:"mod_time"`

	// 文件指纹
	// 如果是本地文件，这里存的是计算后的 MD5
	LocalHash string `json:"local_hash"`

	// 对于百度网盘，这里通常存的是云端返回的 MD5
	RemoteHash string `json:"remote_hash"`

	// 是否为文件夹
	IsDir bool `json:"is_dir"`

	// 最后一次同步的时间 (用于调试或过期策略)
	LastSyncTime int64 `json:"last_sync_time"`
}

// ModTimeAsTime 辅助方法：转为 Go Time 对象
func (f *FileState) ModTimeAsTime() time.Time {
	return time.Unix(0, f.ModTime)
}
