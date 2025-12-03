package fs

import (
	"io"
	"time"
)

// FileMeta 文件元数据
type FileMeta struct {
	RelPath    string    // 相对路径 (统一使用 "/" 作为分隔符)
	Size       int64     // 文件大小
	ModTime    time.Time // 修改时间
	IsDir      bool      // 是否为目录
	Hash       string    //文件hash
	RemoteHash string    //网盘中的文件hash
}

// FileSystem 是对 Local 和 Baidu 的统一抽象
type FileSystem interface {
	// Root 返回该文件系统的根路径 (用于日志或调试)
	Root() string

	// ListAll 递归列出所有文件
	// 返回 map[相对路径]元数据，方便快速查找
	ListAll() (map[string]*FileMeta, error)

	// OpenStream 打开文件流 (用于读取数据)
	// rangeStart: 支持断点续传/分片读取 (如果不需要传0)
	OpenStream(relPath string) (io.ReadCloser, error)

	// WriteStream 写入文件流 (用于保存数据)
	// 包含创建父目录的逻辑
	WriteStream(relPath string, stream io.Reader, perm time.Time) (string, error)

	// Delete 删除文件
	Delete(relPath string) error

	// Stat 获取单个文件信息
	Stat(relPath string) (*FileMeta, error)
	Rename(oldRelPath, newRelPath string) error
}
