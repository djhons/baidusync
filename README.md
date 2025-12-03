# BaiduSync - 百度网盘同步工具

BaiduSync 是一个命令行的、单向或双向的文件同步工具，旨在帮助您将本地文件安全、自动地同步到百度网盘。它支持文件加密、冲突处理和定时同步，特别适合用于备份私人数据和重要文件。

## 主要功能

*   **双向同步**: 自动检测本地和云端的文件变更（增、删、改），并进行同步。
*   **内容加密**: 支持 `AES-256-CTR` 对文件内容进行加密，确保您在云端的隐私安全。
*   **文件名加密**: 可选地对文件名进行加密，进一步保护您的隐私。
*   **冲突处理**: 提供多种冲突解决策略，如 `重命名`、`保留最新` 或 `强制覆盖`。
*   **定时任务**: 可配置定时同步，例如每分钟、每小时或每天。
*   **并发传输**: 支持多个文件并发上传/下载，提高同步效率。
*   **断点续传**: （百度网盘 API 支持）在上传大文件时，如果网络中断，下次可以从断点处继续。
*   **跨平台**: 基于 Go 语言开发，可轻松编译并在 Windows, macOS, Linux 上运行。

## 配置文件

在使用前，您需要配置 `config/config.yaml` 文件。以下是各参数的详细说明：

```yaml
# ==========================================
# BaiduSync 配置文件
# ==========================================

# --- 1. 同步设置 (Synchronization) ---
sync:
  # 本地需要同步的绝对路径
  # Windows 示例: "D:\\Documents\\MyPrivateData"
  # Linux/Mac 示例: "/home/user/private_data"
  local_dir: "./data/"

  # 百度网盘中的目标目录 (通常在 /apps/你的应用名/ 下)
  remote_dir: "/apps/baidusync"

  # 同步检测间隔时间 (支持 s, m, h)
  interval: "1m"

  # 最大并发上传/下载数量
  max_concurrent: 3
  
  # 冲突解决策略
  # rename_local (默认): 冲突时，重命名本地文件为 .local 后缀，并下载云端版本。
  # rename_remote: 冲突时，重命名云端文件为 .remote 后缀，并上传本地版本。
  # keep_latest: 保留时间戳最新的文件。
  # delete_remote: 强制以本地为准，删除云端冲突文件后上传。
  # delete_local: 强制以云端为准，删除本地冲突文件后下载。
  conflict_strategy: rename_local

# --- 2. 百度网盘认证 (Baidu PCS Auth) ---
baidu:
  # 从百度开放平台获取的 AppKey (API Key)
  app_key: "your_app_key"

  # 从百度开放平台获取的 SecretKey
  secret_key: "your_secret_key"

  # 访问令牌 (通过 OAuth2 流程获取)
  access_token: "your_access_token"

  # 刷新令牌 (通过 OAuth2 流程获取，用于自动刷新 AccessToken)
  refresh_token: "your_refresh_token"

  # 伪装的 User-Agent
  user_agent: "pan.baidu.com"

# --- 3. 加密设置 (Encryption) ---
crypto:
  # 是否开启加密
  enable: true

  # 加密密码 (!!!请务必牢记!!!)
  password: "your_strong_password_here"

  # 是否加密文件名
  encrypt_filenames: true

  # 加密算法
  algorithm: "aes-256-ctr"

# --- 4. 系统与存储 (System & Storage) ---
system:
  # 状态数据库路径 (用于记录文件同步状态)
  db_path: "./sync_state.db"
  
  # 临时文件目录 (例如，用于解密或部分下载)
  temp_dir: "./tmp"

  # 日志级别: debug, info, warn, error
  log_level: "info"

  # 日志文件路径
  log_file: "./logs/app.log"
```

**如何获取百度认证信息:**

1.  访问[百度开放平台](http://developer.baidu.com/)，创建一个“PC应用”。
2.  在应用设置中，您将找到 `AppKey` 和 `SecretKey`。
3.  根据百度官方的 OAuth2.0 文档，编写一个简单的脚本或使用第三方工具来获取 `access_token` 和 `refresh_token`。这通常是一个一次性的手动过程。

## 安装与运行

#### 1. 环境准备

确保您已经安装了 Go 语言环境 (建议版本 1.18 或更高)。

#### 2. 克隆与构建

```bash
# 克隆项目 (或者直接下载源码)
git clone <项目仓库地址>
cd baidusync

# 安装依赖
go mod tidy

# 构建应用
go build -o baidusync .
```

#### 3. 运行

完成配置文件的修改后，直接在终端运行：

```bash
# 在 Windows 上
./baidusync.exe

# 在 Linux/Mac 上
./baidusync
```

程序启动后，将立即执行一次同步，然后根据您在 `config.yaml` 中设置的 `interval` 定时执行。所有操作和错误都会记录在您指定的日志文件中。

## 使用说明

*   **首次运行**: 建议先备份您的本地数据。首次运行时，程序会比较本地和云端的文件。如果云端目录为空，它将上传所有本地文件。如果两边都有文件，它会尝试根据文件大小进行“模糊匹配”来建立初始关联，以避免不必要的上传下载。
*   **优雅退出**: 在终端中按 `Ctrl+C`，程序会等待当前正在进行的同步任务完成后再退出，以保证数据状态的一致性。
*   **日志查看**: 所有的同步活动、警告和错误都会被记录在 `logs/app.log` (或您配置的路径) 中。如果同步出现问题，请首先检查日志文件。

## 免责声明

*   **数据安全**: 本工具会直接操作您的本地文件和百度网盘文件。尽管已经过测试，但仍存在潜在的 bug。**强烈建议您在使用前对重要数据进行额外备份**。
*   **账户风险**: 频繁的 API 调用可能违反百度网盘的使用协议。请合理配置同步间隔 `interval` 和并发数 `max_concurrent`，避免因请求过于频繁而导致账户被限制。
*   **自行承担风险**: 本项目作者不对任何因使用本工具而导致的数据丢失、损坏或账户问题负责。请在充分了解其工作原理和潜在风险后使用。
