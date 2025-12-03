package baidu

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

const (
	// OAuthUrl 百度 Token 刷新地址
	OAuthUrl = "https://openapi.baidu.com/oauth/2.0/token"
)

// RefreshToken 主动刷新 AccessToken
func (c *Client) RefreshToken() error {
	params := url.Values{}
	params.Set("grant_type", "refresh_token")
	params.Set("refresh_token", c.opts.RefreshToken)
	params.Set("client_id", c.opts.AppKey)
	params.Set("client_secret", c.opts.SecretKey)

	resp, err := c.httpClient.Get(OAuthUrl + "?" + params.Encode())
	if err != nil {
		return fmt.Errorf("刷新 token 网络请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// 检查是否有错误字段
	var errResp struct {
		Error string `json:"error"`
		Desc  string `json:"error_description"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
		return fmt.Errorf("刷新 token 失败: %s - %s", errResp.Error, errResp.Desc)
	}

	// 解析成功响应
	var authResp AuthResponse
	if err := json.Unmarshal(body, &authResp); err != nil {
		return fmt.Errorf("解析 token 响应失败: %w", err)
	}

	// 更新内存中的 Token
	c.opts.AccessToken = authResp.AccessToken
	c.opts.RefreshToken = authResp.RefreshToken // 刷新 Token 也可能会变

	// TODO: 这里应该回调通知 Config 模块把新 Token 写入 config.yaml 文件持久化
	// c.onTokenUpdate(authResp)

	return nil
}
