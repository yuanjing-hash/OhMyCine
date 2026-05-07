# API 参考

## 插件 API 概述

OhMyCine 插件 API 提供以下能力：

### 配置访问

```go
// GetConfig 获取插件配置
func GetConfig(key string) (interface{}, error)

// SetConfig 设置插件配置
func SetConfig(key string, value interface{}) error
```

### 网络请求

```go
// HTTPClient 获取带超时的 HTTP 客户端
func HTTPClient() *http.Client
```

### 存储

```go
// GetDB 获取插件专属数据库
func GetDB() *gorm.DB
```

### 日志

```go
// Logger 获取结构化日志器
func Logger() *zerolog.Logger
```

### 事件

```go
// Subscribe 订阅事件
func Subscribe(topic string, handler EventHandler) error

// Publish 发布事件
func Publish(topic string, event Event) error
```

> 完整 API 文档将在插件系统实现后补充。第三方插件默认不可信，后续 API 必须通过权限声明、凭据隔离和受控 HTTP Client 暴露能力。
