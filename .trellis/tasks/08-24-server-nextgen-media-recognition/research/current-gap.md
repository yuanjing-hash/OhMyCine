# OhMyCine 当前识别链路与失败根因

## 失败样本

```text
Ming Dynasty in 1566 HQ -BlackTV
```

本机已有运行数据确认：provider 为 `pan115_offline`，下载完成，manifest 有 49 个媒体文件，类型已推断为 `tv`，年份已提取为 `2007`，TMDB 凭据可用，最终错误为 `tmdb_no_match`。因此问题位于候选生成与远端匹配，不是下载、凭据或年份 1566 误解析。

## 标题候选缺口

`server/internal/services/download_title.go`：

- `downloadTechToken` 不含 `HQ`。
- `downloadReleaseGroup` 不含 `BlackTV`。
- `downloadTrailingGroup` 要求连字符两侧都有空格，无法匹配实际的 ` -BlackTV`。
- `downloadSearchTitles` 最多返回三个候选，但本例无法得到 `Ming Dynasty in 1566`。
- 当前规则将资源规格和标题清洗耦合在一组正则中，没有保留每步证据或说明为什么删除某段。

## TMDB 匹配缺口

`server/pkg/metadata/tmdb/client.go:393` 的 `Search` 已比较本地化标题与 `original_title` / `original_name`，这比最初观察更完整；仍存在以下问题：

- 只以 TMDB 返回的第一条结果为主结果，第二条仅用于简单降分。
- 不读取 alternative titles 和 translations 参与召回/排名。
- `titleConfidence` 仅有精确 `.98`、包含 `.82`、其他 `.62` 三档，正规化只处理点、下划线、连字符、大小写和空白。
- 年份只支持精确过滤；无 `±1` 容差，年份过滤导致零结果时没有逐级放宽策略。
- `SearchCandidates` 不解析/比较原标题。

`server/internal/services/media_recognition.go`：

- 依次尝试预先确定类型的标题候选，没有类型未知的 multi-search。
- `lookup.Search` 返回第一条非 `tmdb_no_match` 即停止，候选无法统一排序。
- 置信度低于 `.80` 即失败，但该分数并非经过 corpus 校准。

## 恢复语义缺口

当前 UI 对 294.4 GiB 已完成的 115 离线任务显示“重试下载”。识别失败属于 Import 前的可恢复状态，正确动作应是自动重新识别并继续 Transfer/Import/Notify；仅当自动链仍失败时，才允许高级用户可选填写显式媒体 ID 纠错。重新提交下载既浪费资源也可能制造重复任务。

## 可复用基础

- 下载与媒体库已经共享 `recognizeMedia`，适合继续收敛为单一引擎。
- 已有 provider-neutral 输入边界、识别 cache、显式身份 override、可信 `GetByID`、Profile 识别词包和安全候选 API。
- 现有边界应保留：识别器不接收绝对路径、凭据或 provider ID；显式身份纠错必须重新从 TMDB 验证。现有候选 API 可作为诊断能力，但不进入正常入库交互。
