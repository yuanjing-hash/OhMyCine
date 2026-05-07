# 插件开发快速开始

## 概述

OhMyCine 插件用于扩展 Server 和 Player 的能力。插件类别包括：

- 云盘驱动、PT 站点、下载客户端、元数据源
- 通知服务、UI 扩展、AI 提供者

## 环境准备

- Go 1.22+（Server 插件候选运行时）
- OhMyCine Server 运行实例

> 插件系统仍处于设计阶段。长期安全方向优先考虑 WASM 或外部进程隔离，Go 插件加载仅作为候选方案之一。

## 创建插件

```bash
# 使用 CLI 初始化插件项目
omc plugin init my-plugin
cd my-plugin
```

## 插件结构

```
my-plugin/
├── plugin.yaml       # 插件清单
├── main.go           # 入口
└── README.md         # 文档
```

## 调试

```bash
# 本地加载插件
omc plugin dev ./my-plugin
```

更多规范详见 [插件规范](/dev/spec)。
