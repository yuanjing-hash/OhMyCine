# omc — 命令行工具设计文档

## 1. 概述

`omc` 是 OhMyCine 的命令行管理工具，用于：
- 管理 OhMyCine Server
- 快速操作（搜索、下载、扫描）
- 自动化脚本集成
- 系统诊断

## 2. 技术栈

- Go + Cobra（与Server共享部分代码）
- 单二进制文件，无依赖
- 支持 Tab 补全（Bash/Zsh/Fish/PowerShell）

## 3. 命令设计

```bash
omc — OhMyCine Command Line Interface

Usage:
  omc [command]

Available Commands:
  server      管理 OhMyCine Server
  config      配置管理
  library     媒体库操作
  cloud       网盘管理
  search      资源搜索
  download    下载管理
  plugin      插件管理
  strm        STRM文件操作
  doctor      系统诊断
  version     版本信息

Flags:
  -c, --config string    配置文件路径 (default "~/.omc/config.yaml")
  -s, --server string    Server地址 (default "http://localhost:3000")
  -k, --key string       API Key
  -o, --output string    输出格式 (table/json/yaml) (default "table")
  -v, --verbose          详细输出
  -h, --help             帮助

Use "omc [command] --help" for more information about a command.
```

### 3.1 Server 管理

```bash
# 启动Server
omc server start [--port 3000] [--daemon]

# 停止Server
omc server stop

# 查看状态
omc server status

# 查看日志
omc server logs [--tail 100] [--follow]

# 更新Server
omc server update

# 导出/导入配置
omc server export --output backup.yaml
omc server import --file backup.yaml
```

### 3.2 配置管理

```bash
# 查看所有配置
omc config show

# 获取/设置配置项
omc config get server.port
omc config set server.port 3000

# 配置Server连接
omc config set server.url http://nas.local:3000
omc config set server.api_key xxx

# 配置网盘
omc config set cloud.115.cookie "UID=xxx; CID=xxx; SEID=xxx"

# 配置PT站点
omc config set site.mteam.cookie "xxx"
omc config set site.mteam.passkey "xxx"
```

### 3.3 媒体库操作

```bash
# 列出媒体库
omc library list [--type movie|series] [--sort year|rating|title] [--limit 20]

# 搜索媒体
omc library search "Inception"

# 触发扫描
omc library scan [--path /movies]

# 查看媒体详情
omc library info <media-id>

# 删除媒体
omc library remove <media-id> [--delete-files]
```

### 3.4 网盘管理

```bash
# 列出已配置网盘
omc cloud list

# 添加网盘
omc cloud add --driver 115 --name "我的115" --cookie "UID=xxx"

# 测试连接
omc cloud test <drive-id>

# 浏览网盘文件
omc cloud ls <drive-id> [/path/to/dir]

# 获取配额
omc cloud quota <drive-id>

# 生成STRM
omc cloud strm generate <drive-id> /movies --output /library/movies
```

### 3.5 资源搜索

```bash
# 跨站点搜索
omc search "星际穿越" [--site mteam,hdsky] [--min-size 10G] [--max-size 50G]

# 搜索结果详情
omc search info <result-id>

# 直接下载搜索结果
omc search download <result-id> [--client qbittorrent]
```

### 3.6 下载管理

```bash
# 列出下载任务
omc download list [--status downloading|completed|all]

# 添加种子下载
omc download add <torrent-file-or-magnet> [--save /downloads]

# 暂停/恢复/删除
omc download pause <task-id>
omc download resume <task-id>
omc download remove <task-id>

# 查看下载详情
omc download info <task-id>
```

### 3.7 插件管理

```bash
# 列出已安装插件
omc plugin list

# 安装插件
omc plugin install <plugin-name> [--version 1.2.0]

# 卸载插件
omc plugin remove <plugin-name>

# 启用/禁用
omc plugin enable <plugin-name>
omc plugin disable <plugin-name>

# 查看插件日志
omc plugin logs <plugin-name>

# 搜索插件市场
omc plugin search "115"
```

### 3.8 STRM操作

```bash
# 扫描网盘并生成STRM库
omc strm generate --driver 115 --remote /movies --local /library/movies

# 增量同步
omc strm sync

# 校验STRM文件（检查链接是否有效）
omc strm verify [/library/movies]

# 修复无效STRM
omc strm repair [/library/movies]
```

### 3.9 系统诊断

```bash
# 全面诊断
omc doctor

# 输出示例:
# ✓ Server connection: OK (http://localhost:3000)
# ✓ Database: OK (SQLite, 45MB)
# ✓ Emby connection: OK (http://localhost:8096)
# ✓ Cloud drive - 115: OK (2.3TB / 5TB used)
# ✗ Cloud drive - Aliyun: Cookie expired
# ✓ qBittorrent: OK (3 active downloads)
# ⚠ STRM library: 12 broken links found
# ✓ MPV binary: OK (v0.37.0)
# ✓ TMDB API: OK

# 单项检查
omc doctor server
omc doctor cloud
omc doctor strm
omc doctor emby
```

## 4. 输出格式

```bash
# 默认表格输出
$ omc library list --type movie --limit 5
┌────────────────────────────┬──────┬──────────┬────────┐
│ Title                      │ Year │ Rating   │ Quality│
├────────────────────────────┼──────┼──────────┼────────┤
│ Inception                  │ 2010 │ ⭐ 8.8   │ 4K DV  │
│ Interstellar               │ 2014 │ ⭐ 8.7   │ 4K HDR │
│ The Dark Knight            │ 2008 │ ⭐ 9.0   │ 1080p  │
│ Parasite                   │ 2019 │ ⭐ 8.5   │ 4K     │
│ Oppenheimer                │ 2023 │ ⭐ 8.4   │ 4K DV  │
└────────────────────────────┴──────┴──────────┴────────┘

# JSON输出 (用于脚本)
$ omc library list --type movie --limit 2 -o json
[
  {"id": 1, "title": "Inception", "year": 2010, "rating": 8.8, "quality": "4K DV"},
  {"id": 2, "title": "Interstellar", "year": 2014, "rating": 8.7, "quality": "4K HDR"}
]
```

## 5. Shell 补全

```bash
# Bash
omc completion bash > /etc/bash_completion.d/omc

# Zsh
omc completion zsh > "${fpath[1]}/_omc"

# Fish
omc completion fish > ~/.config/fish/completions/omc.fish

# PowerShell
omc completion powershell | Out-String | Invoke-Expression
```
