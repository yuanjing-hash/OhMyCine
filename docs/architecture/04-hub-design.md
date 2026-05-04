# OhMyCine Hub — 插件市场设计文档

## 1. 概述

OhMyCine Hub 是插件生态的分发平台，提供：
- 插件浏览、搜索、安装
- 插件版本管理
- 社区评分与评论
- 开发者文档

## 2. 技术方案

Hub 采用**静态站点 + GitHub Registry**模式，无需独立服务器：

```
┌──────────────────┐     ┌────────────────────┐
│   Hub Website    │     │  GitHub Releases   │
│  (VitePress/Vue) │────►│  插件包托管         │
│   静态站点       │     │  manifest.json     │
└──────────────────┘     └────────────────────┘
        │                          │
        │   浏览/搜索              │  下载安装
        ▼                          ▼
┌──────────────────────────────────────────────┐
│              OhMyCine Server                  │
│         Plugin Engine (插件引擎)              │
│         /api/v1/plugins/install              │
└──────────────────────────────────────────────┘
```

## 3. 插件规范

### 3.1 插件包结构

```
my-plugin.zip
├── manifest.yaml          # 插件元信息
├── plugin.go              # Go插件源码（编译为.so/.dylib）
├── web/                   # 可选：Web UI组件
│   └── index.vue
├── configs/               # 默认配置
│   └── config.yaml
└── README.md
```

### 3.2 manifest.yaml

```yaml
name: "cloud-115-enhanced"
version: "1.2.0"
display_name: "115网盘增强"
description: "115网盘高级功能：批量STRM生成、秒传、目录同步"
author: "community"
license: "GPL-3.0"
min_server_version: "0.1.0"

# 插件类型
type: "driver"  # driver / scraper / metadata / download / transfer / notification / player / ai / ui

# 依赖
dependencies:
  - name: "cloud-base"
    version: ">=1.0.0"

# 配置项定义
config_schema:
  - key: "cookie"
    label: "115 Cookie"
    type: "password"
    required: true
  - key: "sync_interval"
    label: "同步间隔"
    type: "select"
    options: ["1h", "6h", "12h", "24h"]
    default: "6h"

# 钩子
hooks:
  - event: "file.added"
    handler: "OnFileAdded"
  - event: "download.completed"
    handler: "OnDownloadCompleted"

# 资源
resources:
  cpu: "low"
  memory: "64MB"
  network: true
```

### 3.3 插件接口

```go
// OhMyCinePlugin 所有插件必须实现的基础接口
type OhMyCinePlugin interface {
    // 基础信息
    Name() string
    Version() string
    Description() string

    // 生命周期
    Init(ctx *PluginContext) error
    Start() error
    Stop() error

    // 事件处理
    HandleEvent(event string, data interface{}) error
}

// WebPlugin 带 Web UI 的插件接口
type WebPlugin interface {
    OhMyCinePlugin
    RegisterRoutes(router *gin.RouterGroup)
}
```

## 4. 预置插件类型

| 类型 | 说明 | 示例 |
|------|------|------|
| `driver` | 网盘驱动 | 115网盘、阿里云盘、夸克 |
| `scraper` | PT站点刮削器 | M-Team、HDSky、OurBits |
| `metadata` | 元数据源 | TMDB、豆瓣、Bangumi |
| `download` | 下载客户端 | qBittorrent、Transmission、aria2 |
| `transfer` | 转移策略 | 硬链接、软链接、云盘直传 |
| `notification` | 通知渠道 | Telegram Bot、Bark、Server酱、Webhook |
| `player` | 播放器扩展 | 弹幕、歌词、特效 |
| `ai` | AI提供商 | OpenAI、Claude、本地LLM |
| `ui` | UI扩展 | 自定义主题、自定义首页组件 |

## 5. Hub 网站功能

### 5.1 页面结构

```
/
├── /plugins                 # 插件列表（分类、搜索、排序）
├── /plugins/:name           # 插件详情页
├── /docs                    # 开发者文档
│   ├── /docs/getting-started
│   ├── /docs/plugin-api
│   ├── /docs/driver-dev
│   ├── /docs/scraper-dev
│   └── /docs/examples
└── /changelog               # 更新日志
```

### 5.2 插件详情页

```
┌────────────────────────────────────────────────────┐
│  115网盘增强                          ⭐ 4.8 (126) │
│  Cloud 115 Enhanced    v1.2.0   by community       │
│                                                    │
│  [Install]  [Source Code]  [Report Issue]          │
├────────────────────────────────────────────────────┤
│  Description                                       │
│  115网盘高级功能：批量STRM生成、秒传、目录同步      │
│                                                    │
│  Features:                                         │
│  - 批量STRM文件生成                                │
│  - SHA1秒传加速                                    │
│  - 定时目录同步                                    │
│  - 302直连播放优化                                  │
├────────────────────────────────────────────────────┤
│  Configuration                                     │
│  ┌──────────────────────────────────┐              │
│  │ Cookie: [••••••••••••]           │              │
│  │ Sync Interval: [6h ▼]            │              │
│  └──────────────────────────────────┘              │
├────────────────────────────────────────────────────┤
│  Reviews (126)                                     │
│  ⭐⭐⭐⭐⭐ 张三: STRM生成太好用了                   │
│  ⭐⭐⭐⭐   李四: 希望增加秒传功能                    │
└────────────────────────────────────────────────────┘
```

## 6. 插件安装流程

```
用户在Hub网站浏览插件
    │
    │ 点击 [Install]
    ▼
Hub生成安装命令/链接
    │
    │ 方式1: omc plugin install cloud-115-enhanced
    │ 方式2: OhMyCine Server API: POST /api/v1/plugins/install
    │ 方式3: 播放器UI中一键安装
    ▼
Server下载插件包
    │
    │ 1. 从GitHub Release下载zip
    │ 2. 校验hash签名
    │ 3. 解压到 plugins/ 目录
    │ 4. 加载 manifest.yaml
    │ 5. 编译Go插件 (.so/.dylib)
    │ 6. 调用 Init() + Start()
    ▼
插件运行中
    │
    │ 通过事件总线与系统交互
    ▼
用户通过Web UI/播放器配置插件
```
