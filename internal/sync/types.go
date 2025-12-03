package sync

// OpType 定义同步操作类型
type OpType int

const (
	OpIgnore       OpType = iota // 忽略 (两边一致)
	OpUpload                     // 上传 (本地 -> 网盘)
	OpDownload                   // 下载 (网盘 -> 本地)
	OpDeleteRemote               // 删除网盘文件
	OpDeleteLocal                // 删除本地文件
	OpConflict                   // 冲突 (通常重命名本地文件后下载)
)

// Task 代表一个具体的同步任务
type Task struct {
	Op      OpType
	RelPath string // 相对路径
	Reason  string // 触发原因 (用于日志)
}
