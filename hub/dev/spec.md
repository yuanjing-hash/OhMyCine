# 插件规范

## 清单文件 (plugin.yaml)

```yaml
name: my-plugin
version: 1.0.0
author: Your Name
description: 插件描述
category: cloud-driver  # cloud-driver | pt-site | downloader | metadata | notification | ui | ai-provider
permissions:
  - network           # 网络访问
  - storage           # 存储访问
  - config:read       # 读取配置
  - config:write      # 写入配置
```

## 生命周期

```go
// 伪代码示例：最终运行时可能是 WASM、外部进程或 Go 插件。
// Init — 插件初始化，加载配置
func Init(ctx context.Context, config map[string]interface{}) error

// Start — 插件启动，注册服务
func Start(ctx context.Context) error

// Stop — 插件停止，清理资源
func Stop(ctx context.Context) error
```

## 事件总线

插件可通过事件总线与其他插件或核心系统通信：

```go
// 订阅事件
bus.Subscribe("download.completed", func(event Event) {
    // 处理下载完成事件
})

// 发布事件
bus.Publish("media.added", Event{
    Data: map[string]interface{}{...},
})
```

## 安全

- 插件必须声明所需权限
- 用户确认后方可启用
- 插件不得访问未授权的凭据
- 第三方插件默认隔离运行
