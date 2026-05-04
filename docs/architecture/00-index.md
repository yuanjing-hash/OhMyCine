# OhMyCine 设计文档索引

> THE NORTH STAR OF YOUR CINEMA

## 文档目录

| 编号 | 文档 | 说明 |
|------|------|------|
| 00 | [索引](00-index.md) | 本文件 |
| 01 | [产品架构总览](01-overview.md) | 产品愿景、系统架构、技术选型、部署模式 |
| 02 | [Server后端设计](02-server-design.md) | 媒体流水线(发现→下载→转移→入库)、三层架构、追更、302代理、STRM管理、API设计、数据库 |
| 03 | [Player播放器设计](03-player-design.md) | Tauri+Vue前端、libmpv集成、DataSource抽象层、自动刮削、AI推荐、Cinema OS UI |
| 04 | [Hub插件市场设计](04-hub-design.md) | 插件规范、安装流程、Hub网站 |
| 05 | [CLI命令行设计](05-cli-design.md) | omc命令体系、Shell补全 |
| 06 | [开发路线图](06-roadmap.md) | 4阶段开发计划、里程碑、风险评估 |

## 快速开始

### 产品矩阵

```
OhMyCine
├── Player    — 跨平台播放器 (Tauri v2 + Vue 3 + libmpv)
├── Server    — 媒体流水线后端 (Go + Gin + GORM + SQLite)
├── Hub       — 插件市场 (VitePress)
└── omc       — 命令行工具 (Go + Cobra)
```

### 核心技术选型

```
Frontend:  Tauri v2 + Vue 3 + TypeScript + UnoCSS
Backend:   Go + Gin + GORM + SQLite
Playback:  libmpv (嵌入式, 全平台)
Deploy:    Docker + 二进制文件
License:   GPL-3.0
```

### 设计原则

1. **用户数据主权** — 数据在用户自己的服务器上
2. **模块化可插拔** — 每个组件可独立使用
3. **开源优先** — GPL-3.0 License，社区驱动
4. **性能至上** — 302直连、MPV引擎、零丢帧
