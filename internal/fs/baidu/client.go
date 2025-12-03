package baidu

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// PCSBaseURL 基础 API 地址
	PCSBaseURL = "https://pan.baidu.com/rest/2.0/xpan/file"
	// PCSUploadURL 上传专用地址 (部分文档指向 d.pcs.baidu.com)
	PCSUploadURL = "https://d.pcs.baidu.com/rest/2.0/pcs/file"
	// BlockSize 百度网盘分片大小 (4MB)
	BlockSize = 4 * 1024 * 1024

	// PCSUploadURL 分片上传专用 URL (Superfile2)
	PCSSuperfileURL = "https://pcs.baidu.com/rest/2.0/pcs/superfile2"
)

// Options 初始化参数
type Options struct {
	AppKey       string
	SecretKey    string
	AccessToken  string
	RefreshToken string
	UserAgent    string
}

// Client 百度网盘 HTTP 客户端
type Client struct {
	opts       *Options
	httpClient *http.Client
}

// NewClient 创建客户端
func NewClient(opts *Options) *Client {
	if opts.UserAgent == "" {
		opts.UserAgent = "pan.baidu.com" // 防止被屏蔽
	}
	return &Client{
		opts: opts,
		httpClient: &http.Client{
			Timeout: 60 * time.Second, // 基础超时，下载/上传时 context 控制
		},
	}
}

// ListDir 列出目录下的文件
func (c *Client) ListDir(remoteDir string) ([]FileInfo, error) {
	params := url.Values{}
	params.Set("method", "list")
	params.Set("dir", remoteDir)
	params.Set("limit", "1000") // 简单起见，暂不处理分页

	body, err := c.request("GET", PCSBaseURL, params, nil)
	if err != nil {
		return nil, err
	}

	var resp ListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("api error: %d %s", resp.ErrNo, resp.Msg)
	}

	return resp.List, nil
}

// Download 下载文件流
func (c *Client) Download(remotePath string) (io.ReadCloser, error) {
	params := url.Values{}
	params.Set("method", "download")
	params.Set("path", remotePath)
	params.Set("access_token", c.opts.AccessToken)

	reqUrl := PCSBaseURL + "?" + params.Encode()
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.opts.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	// 调用者负责 Close
	return resp.Body, nil
}

// Delete 删除文件或目录
// remotePath: 要删除的文件或目录的百度网盘路径。
// 注意：该函数假设您的 Client 结构体中包含 access_token，并且 c.request 能够处理 HTTP 请求。
func (c *Client) Delete(remotePath string) error {
	// 1. 构造请求体 (Request Body)
	// 百度网盘的 delete 接口需要将文件路径列表作为一个 JSON 数组字符串放在 POST 请求体中。
	// async=0 表示同步删除，async=1/2 表示异步删除。这里使用同步(0)以获取即时结果。
	// 若要实现 curl 示例中的异步删除 (async=2)，请将 "0" 改为 "2"。

	// 使用 string 数组构造 JSON 结构，然后序列化，避免手动拼接字符串的转义问题。
	fileList := []string{remotePath}
	fileListJSON, err := json.Marshal(fileList)
	if err != nil {
		return fmt.Errorf("failed to marshal file list to JSON: %w", err)
	}

	// 构造 POST 请求的 body 数据 (x-www-form-urlencoded 格式)
	data := url.Values{}
	data.Set("async", "2")
	data.Set("filelist", string(fileListJSON))

	// 2. 构造请求参数 (URL Query Parameters)
	params := url.Values{}
	params.Set("method", "filemanager")
	params.Set("opera", "delete")

	// 假设 Client 结构体中已有 access_token 并会在 c.request 中自动添加
	// 如果没有，需要在这里显式添加：
	// params.Set("access_token", c.AccessToken)

	// 3. 发送请求
	// c.request(method, url, queryParams, bodyData)
	body, err := c.request("POST", PCSBaseURL, params, strings.NewReader(data.Encode()))
	if err != nil {
		return err // 错误已在 c.request 中处理
	}

	// 4. 解析响应
	var resp PCSResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 检查百度网盘接口返回的错误码
	if !resp.IsSuccess() { // 假设 IsSuccess() 检查 resp.ErrNo == 0
		return fmt.Errorf("delete operation failed: ErrNo=%d, Msg=%s", resp.ErrNo, resp.Msg)
	}

	// 如果是异步删除 (async=1/2)，resp 中可能包含 task_id 等信息，可以返回或记录。
	// 对于同步删除 (async=0)，成功即表示删除完成。

	return nil
}

// request 通用请求封装
func (c *Client) request(method, urlStr string, params url.Values, body io.Reader) ([]byte, error) {
	// 自动注入 AccessToken
	if params == nil {
		params = url.Values{}
	}
	params.Set("access_token", c.opts.AccessToken)

	fullURL := urlStr + "?" + params.Encode()

	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// Upload 执行由 Precreate -> Superfile2 -> Create 组成的大文件上传流程
// content: 输入流 (可能是加密流)
// _ : 原始大小 (忽略，以加密后落地的临时文件大小为准)
func (c *Client) Upload(remotePath string, content io.Reader, _ int64) (string, error) {
	// 1. 【创建临时文件】
	// 由于 content 可能是不可回退的加密流，而分片上传需要先计算全量 MD5 再分片读取
	tmpFile, err := os.CreateTemp("", "cloudsync_upload_*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer func() {
		tmpFile.Close()
		os.Remove(tmpFile.Name()) // 上传结束后清理
	}()

	// 2. 【写入数据并获取真实大小】
	size, err := io.Copy(tmpFile, content)
	if err != nil {
		return "", fmt.Errorf("写入临时文件失败: %w", err)
	}

	// 重置文件指针到开头
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return "", fmt.Errorf("seek tmpfile failed: %w", err)
	}

	// 3. 【计算指纹】
	// 获取分片 MD5 列表和 全量 MD5 (localTotalMD5 用于最后校验)
	blockMD5s, _, err := c.calculateFingerprint(tmpFile, size)
	if err != nil {
		return "", fmt.Errorf("计算文件指纹失败: %w", err)
	}

	// 4. Step 1: Precreate (预上传)
	uploadID, err := c.precreate(remotePath, size, blockMD5s)
	if err != nil {
		return "", fmt.Errorf("precreate failed: %w", err)
	}

	// 5. Step 2: Upload Slice (分片上传)
	// 如果 uploadID 为空，说明触发了“秒传”，无需上传物理数据
	if uploadID != "" {
		for i := 0; i < len(blockMD5s); i++ {
			offset := int64(i) * BlockSize
			currentBlockSize := int64(BlockSize)
			if offset+currentBlockSize > size {
				currentBlockSize = size - offset
			}

			// 使用 SectionReader 读取指定分片
			sectionReader := io.NewSectionReader(tmpFile, offset, currentBlockSize)

			// 执行分片上传，并获取云端返回的 MD5
			cloudSliceMD5, err := c.uploadSlice(remotePath, uploadID, i, sectionReader, currentBlockSize)
			if err != nil {
				return "", fmt.Errorf("上传分片 %d/%d 失败: %w", i+1, len(blockMD5s), err)
			}

			// 【关键校验 1】: 校验分片 MD5
			// blockMD5s[i] 是我们在 calculateFingerprint 中计算的本地分片 MD5
			if cloudSliceMD5 != blockMD5s[i] {
				return "", fmt.Errorf("分片 %d 数据校验失败: 本地MD5(%s) != 云端MD5(%s)",
					i, blockMD5s[i], cloudSliceMD5)
			}
		}
	}

	// 6. Step 3: Create (合并文件)
	// 假设 c.create 已经根据之前的优化修改为返回 (md5, size, error)
	cloudMD5, cloudSize, err := c.create(remotePath, size, uploadID, blockMD5s)

	if err != nil {
		return cloudMD5, fmt.Errorf("合并文件失败: %w", err)
	}

	// 7. 【关键校验 2】: 校验文件大小
	// 对比本地加密文件大小和云端合并后的大小
	if cloudSize != size {
		return "", fmt.Errorf("文件大小校验失败: 本地(%d) != 云端(%d)", size, cloudSize)
	}

	return cloudMD5, nil
}

func (c *Client) calculateFingerprint(f *os.File, size int64) ([]string, string, error) {
	var blockMD5s []string

	// totalHash := md5.New() // 已移除：不再计算总文件 MD5

	buf := make([]byte, BlockSize)

	// 确保从头开始读
	if _, err := f.Seek(0, 0); err != nil {
		return nil, "", err
	}

	for {
		n, err := io.ReadFull(f, buf)
		if n == 0 {
			break
		}
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, "", err
		}

		// 分片 MD5
		blockHash := md5.Sum(buf[:n])
		blockMD5s = append(blockMD5s, hex.EncodeToString(blockHash[:]))

		// 总 MD5 的代码已移除
		// totalHash.Write(buf[:n])
	}

	// 处理空文件：如果文件大小为 0，添加一个空文件的 MD5 值到分片列表中
	if size == 0 {
		emptyHash := md5.Sum(nil)
		blockMD5s = append(blockMD5s, hex.EncodeToString(emptyHash[:]))
	}

	// 返回分片 MD5 列表，第二个参数（总 MD5）返回空字符串 ""
	return blockMD5s, "", nil
}

// precreate 预上传
func (c *Client) precreate(remotePath string, size int64, blockMD5s []string) (string, error) {
	blockListJSON, _ := json.Marshal(blockMD5s)

	params := url.Values{}
	params.Set("method", "precreate")

	// 构造 Form 数据
	data := url.Values{}
	data.Set("path", remotePath)
	data.Set("size", fmt.Sprintf("%d", size))
	data.Set("isdir", "0")
	data.Set("autoinit", "1")
	data.Set("rtype", "3") // 3=覆盖
	data.Set("block_list", string(blockListJSON))

	body, err := c.request("POST", PCSBaseURL, params, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", err
	}

	// 解析响应
	var resp struct {
		PCSResponse
		UploadID   string `json:"uploadid"`
		ReturnType int    `json:"return_type"` // 1=上传部分, 2=秒传
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}

	if !resp.IsSuccess() {
		return "", fmt.Errorf("precreate error: %d %s", resp.ErrNo, resp.Msg)
	}

	return resp.UploadID, nil
}

// uploadSlice 上传单个分片
// 返回: (cloudSliceMD5, error)
func (c *Client) uploadSlice(remotePath string, uploadID string, partSeq int, reader io.Reader, size int64) (string, error) {
	params := url.Values{}
	params.Set("method", "upload")
	params.Set("access_token", c.opts.AccessToken)
	params.Set("type", "tmpfile")
	params.Set("path", remotePath)
	params.Set("uploadid", uploadID)
	params.Set("partseq", strconv.Itoa(partSeq))

	fullURL := PCSSuperfileURL + "?" + params.Encode()

	// 使用 bytes.Buffer 构造 Multipart Body
	// 必须这样做，因为百度要求 Content-Length，而 io.Pipe 产生的是 Chunked 传输
	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)

	part, err := writer.CreateFormFile("file", "blob")
	if err != nil {
		return "", err
	}

	if _, err := io.CopyN(part, reader, size); err != nil {
		return "", err
	}

	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", fullURL, bodyBuf)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", c.opts.UserAgent) //

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("upload slice http status %d", resp.StatusCode)
	}

	// 解析响应，获取 MD5
	var res UploadSliceResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("decode slice response failed: %w", err)
	}

	if res.ErrNo != 0 {
		return "", fmt.Errorf("upload slice errno: %d", res.ErrNo)
	}

	// 返回云端计算的分片 MD5
	return res.MD5, nil
}

// create 合并分片文件
// 返回: (cloudMD5, cloudSize, error)
func (c *Client) create(remotePath string, size int64, uploadID string, blockMD5s []string) (string, int64, error) {
	// 1. 序列化分片 MD5 列表
	blockListJSON, err := json.Marshal(blockMD5s)
	if err != nil {
		return "", 0, fmt.Errorf("marshal block list failed: %w", err)
	}

	params := url.Values{}
	params.Set("method", "create")

	// 2. 构造表单数据
	data := url.Values{}
	data.Set("path", remotePath)
	data.Set("size", fmt.Sprintf("%d", size))
	data.Set("isdir", "0")
	data.Set("uploadid", uploadID)
	data.Set("rtype", "3") // 3=覆盖, 0=遇到同名报错
	data.Set("block_list", string(blockListJSON))

	// 3. 发送请求
	// 注意：data.Encode() 返回的是 urlencoded 字符串，使用 strings.NewReader 效率略高
	body, err := c.request("POST", PCSBaseURL, params, strings.NewReader(data.Encode()))
	if err != nil {
		return "", 0, err
	}

	// 4. 解析响应
	var resp CreateFileResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", 0, fmt.Errorf("unmarshal create response failed: %w", err)
	}

	// 5. 检查错误码
	if !resp.IsSuccess() {
		return "", 0, fmt.Errorf("create file error: errno=%d msg=%s", resp.ErrNo, resp.Msg)
	}

	// 6. 返回关键元数据 (MD5 和 Size)
	return resp.MD5, resp.Size, nil
}

// Rename 重命名或移动文件
// oldPath: 原文件绝对路径
// newName: 新文件名 (注意：百度 API 的 rename 参数只需要新名字，不需要完整路径)
func (c *Client) Rename(oldPath string, newName string) error {
	// 1. 准备 URL 参数
	query := url.Values{}
	query.Set("method", "filemanager")
	query.Set("access_token", c.opts.AccessToken)

	// 2. 准备 Body 参数
	// 格式: [{"path":"/old/path","newname":"new_name"}]
	fileList := []map[string]string{
		{
			"path":    oldPath,
			"newname": newName,
		},
	}
	fileListBytes, err := json.Marshal(fileList)
	if err != nil {
		return fmt.Errorf("marshal filelist failed: %w", err)
	}

	form := url.Values{}
	form.Set("opera", "rename")
	form.Set("async", "0")
	form.Set("filelist", string(fileListBytes))

	// 3. 发送请求
	fullURL := PCSBaseURL + "?" + query.Encode()
	req, err := http.NewRequest("POST", fullURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.opts.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var pcsResp PCSResponse
	if err := json.Unmarshal(body, &pcsResp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if !pcsResp.IsSuccess() && pcsResp.ErrNo != 0 {
		return fmt.Errorf("rename error: %d %s", pcsResp.ErrNo, pcsResp.Msg)
	}

	return nil
}
