package baidu

// PCSResponse 通用响应外壳
type PCSResponse struct {
	ErrNo int    `json:"errno"`
	Msg   string `json:"errmsg"`
}

// IsSuccess 判断请求是否成功
func (r *PCSResponse) IsSuccess() bool {
	return r.ErrNo == 0
}

// FileInfo 百度返回的文件信息
type FileInfo struct {
	FsID        uint64 `json:"fs_id"`
	Path        string `json:"path"`
	ServerName  string `json:"server_filename"`
	Size        int64  `json:"size"`
	ServerMTime int64  `json:"server_mtime"` // Unix 时间戳
	ServerCTime int64  `json:"server_ctime"`
	LocalMTime  int64  `json:"local_mtime"`
	IsDir       int    `json:"isdir"` // 0:文件, 1:目录
	MD5         string `json:"md5"`
}

// CreateFileResponse 对应 create 接口的返回 JSON
type CreateFileResponse struct {
	PCSResponse        // 继承 ErrNo 和 Msg
	FsID        uint64 `json:"fs_id"`
	MD5         string `json:"md5"`
	Size        int64  `json:"size"`
	Path        string `json:"path"`
	Ctime       int64  `json:"ctime"`
	Mtime       int64  `json:"mtime"`
	IsDir       int    `json:"isdir"`
}

// ListResponse /file?method=list 响应
type ListResponse struct {
	PCSResponse
	List []FileInfo `json:"list"`
}
type UploadSliceResponse struct {
	MD5       string `json:"md5"`        // 云端计算出的该分片 MD5
	RequestID int64  `json:"request_id"` // 请求 ID，用于调试
	ErrNo     int    `json:"errno"`      // 错误码，0 为成功
}

// UploadResponse /file?method=upload 响应
type UploadResponse struct {
	PCSResponse
	Path string `json:"path"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// AuthResponse 刷新 Token 时的响应
type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}
