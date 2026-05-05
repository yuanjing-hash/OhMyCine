<div align="center">

# OhMyCine

**THE NORTH STAR OF YOUR CINEMA**

开源、全平台、自托管的家庭影院生态系统

[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![GitHub Stars](https://img.shields.io/github/stars/yuanjing-hash/OhMyCine?style=social)](https://github.com/yuanjing-hash/OhMyCine)

</div>

---

## 简介

OhMyCine 是一个**开源、全平台、自托管**的家庭影院生态系统，让你在自己的服务器上构建完整的影视管理与播放系统。

**核心特点**：
- **沉浸式播放器** — Cinema OS 风格 UI，液态玻璃设计语言，libmpv 引擎全格式支持
- **自动化媒体流水线** — 发现→下载→转移→入库→通知，一键闭环
- **智能追更** — 自动追踪剧集更新，缺集自动下载
- **302 直连播放** — 网盘文件零带宽消耗，CDN 直接串流
- **全平台** — Windows, macOS, Linux, Android, iOS

## 产品矩阵

```
OhMyCine
├── Player    — 跨平台沉浸式播放器 (Tauri v2 + Vue 3 + libmpv)
├── Server    — 媒体流水线后端 (Go + Gin + SQLite)
├── Hub       — 插件市场 (VitePress)
└── omc       — 命令行工具 (Go + Cobra)
```

| 组件 | 定位 | 技术栈 | 状态 |
|------|------|--------|------|
| **Player** | 跨平台播放器，独立可用 | Tauri v2 + Vue 3 + TypeScript + libmpv | 开发中 |
| **Server** | 媒体流水线后端 | Go + Gin + GORM + SQLite (PostgreSQL可选) | 开发中 |
| **Hub** | 插件分发平台 | VitePress | 规划中 |
| **omc** | 命令行管理工具 | Go + Cobra | 规划中 |

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                    OhMyCine Player (独立运行)                 │
│                    Tauri v2 + Vue 3 + libmpv                 │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │              DataSourceManager (数据源管理器)           │  │
│  │       统一接口: list / search / getDetail / getStreamURL │  │
│  └─────┬─────────┬─────────┬─────────┬─────────┬────────┘  │
│        │         │         │         │         │            │
│  ┌─────▼──┐ ┌────▼───┐ ┌───▼────┐ ┌──▼─────┐ ┌▼────────┐ │
│  │  Emby  │ │Jellyfin│ │ Alist  │ │CloudDrv│ │ Server  │ │
│  │ 原生API│ │原生API │ │ WebDAV │ │   2    │ │ (可选)  │ │
│  └────────┘ └────────┘ └────────┘ └────────┘ └─────────┘ │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    OhMyCine Server (可选部署)                 │
│                                                             │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐   │
│  │ 连接管理 │ │ 存储目标 │ │ 分类规则 │ │ 发现页       │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────────┘   │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐   │
│  │ 站点管理 │ │ 下载器   │ │ 302代理  │ │ 追更引擎     │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────────┘   │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐                    │
│  │ STRM管理 │ │ 元数据   │ │ 用户管理 │                    │
│  └──────────┘ └──────────┘ └──────────┘                    │
└─────────────────────────────────────────────────────────────┘
```

**数据流**：发现页聚合搜索 → 下载器下载 → 自动分类 → 转移到存储目标 → 生成 STRM(网盘) → 通知 Emby/Jellyfin 刷新 → Player 展示新媒体

## 快速开始

### Player Only（仅播放器）

```bash
# 克隆仓库
git clone https://github.com/yuanjing-hash/OhMyCine.git
cd OhMyCine/player

# 安装依赖
npm install

# 开发模式
npm run tauri dev
```

### 全栈部署（Player + Server）

```bash
cd OhMyCine/server

# Docker Compose 一键启动
docker compose up -d
```

启动后访问：
- Server API: `http://localhost:3000`
- Emby: `http://localhost:8096`（可选）
- qBittorrent: `http://localhost:8080`（可选）

## 技术栈

| 层级 | 技术 |
|------|------|
| Player UI | Tauri v2 + Vue 3 + TypeScript + UnoCSS |
| Player 播放 | libmpv (嵌入式，Rust FFI) |
| Player 动画 | Motion Vue + GSAP |
| Server | Go + Gin + GORM + SQLite |
| Server 定时任务 | robfig/cron |
| CLI | Go + Cobra |
| 文档 | VitePress |
| CI/CD | GitHub Actions |
| 部署 | Docker + Docker Compose |

## 项目结构

```
OhMyCine/
├── player/              — Tauri + Vue 播放器
│   ├── src/             — Vue 前端
│   └── src-tauri/       — Rust 后端 (libmpv 集成)
├── server/              — Go 后端服务
│   ├── cmd/             — 入口
│   ├── internal/        — 私有代码 (handlers, services, models)
│   └── pkg/             — 公共代码 (cloud, downloader, scraper)
├── hub/                 — VitePress 插件市场
├── cli/                 — omc 命令行工具
├── docs/                — 架构文档
│   └── architecture/    — 设计文档 (总览/Server/Player/Hub/CLI/路线图)
└── .github/             — CI/CD Actions
```

## 部署模式

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| **Player Only** | 仅播放器，连接已有 Emby/Alist | 轻量使用 |
| **Player + Server** | 播放器 + 后端，分离部署 | 推荐方案 |
| **全栈** | Docker Compose 一键部署所有服务 | NAS/服务器 |
| **Server Only** | 仅后端 + Emby/Jellyfin 官方客户端 | 服务端管理 |

## 设计文档

详细架构设计请查看 [docs/architecture/](docs/architecture/)：

- [产品架构总览](docs/architecture/01-overview.md)
- [Server 后端设计](docs/architecture/02-server-design.md)
- [Player 播放器设计](docs/architecture/03-player-design.md)
- [Hub 插件市场设计](docs/architecture/04-hub-design.md)
- [CLI 命令行设计](docs/architecture/05-cli-design.md)
- [开发路线图](docs/architecture/06-roadmap.md)
- [安全设计](docs/architecture/07-security-design.md)

## 开发规范

请阅读 [DEVELOPMENT.md](DEVELOPMENT.md) 了解编码规范、Git 工作流、提交规范。

## 贡献

欢迎贡献！请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 了解如何参与开发。

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'feat: add amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

## 许可证

本项目采用 [GPL-3.0 License](LICENSE) 开源。

---

<div align="center">

**OhMyCine** — 让你的影视库成为你的私人影院

</div>
