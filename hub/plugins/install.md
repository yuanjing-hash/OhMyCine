# 安装指南

## 通过 Server 安装

```bash
# 安装插件
omc plugin install <plugin-name>

# 查看已安装插件
omc plugin list

# 卸载插件
omc plugin uninstall <plugin-name>
```

## 手动安装

1. 下载插件包（`.zip` 或 `.tar.gz`）
2. 解压到 `~/.ohmycine/plugins/` 目录
3. 重启 OhMyCine Server

## 权限说明

插件安装后需要声明所需权限，用户确认后方可启用。详见 [插件规范](/dev/spec)。
