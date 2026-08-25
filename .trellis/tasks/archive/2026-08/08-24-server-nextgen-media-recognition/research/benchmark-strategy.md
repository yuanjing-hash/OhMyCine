# 识别器 Benchmark 策略

## 为什么必须先有基准

“比所有识别器都厉害”不能靠增加词表证明。标题解析是高误判风险系统：提升召回很容易，但错配后会造成错误目录、错误 NFO、错误海报以及 Emby/Player 展示污染。必须同时衡量正确率、召回率和误匹配率。

## 语料结构

每条 fixture 使用安全、provider-neutral 字段：

```yaml
id: tv-en-number-title-release-group
package_name: Ming Dynasty in 1566 HQ -BlackTV
files:
  - relative_path: Ming Dynasty in 1566/01.mkv
    size: 123456789
expected:
  media_type: tv
  canonical_title: Ming Dynasty in 1566
  year: 2007
  tmdb_id: <冻结后的正确 ID>
policy: must_match
```

不得保存绝对路径、115 文件 ID、cookie、签名 URL 或真实私有目录名。

## 分层指标

1. Parser：标题、年份、季集、发布组、资源规格、类型证据各字段准确率。
2. Retrieval：正确身份是否进入 Top-1/Top-3/Top-5。
3. Ranking：Top-1 正确率、Top-1/Top-2 分差校准。
4. Safety：误匹配率、应拒绝样本的错误自动入库率。
5. Operations：TMDB 请求数、cache hit、P50/P95/P99、每个 manifest CPU/内存上限。

## 对比方式

- Baseline A：当前 OhMyCine 识别器。
- Baseline B：MoviePilot v3 可公开运行的 Meta 解析输出和 TMDB 匹配行为；只比较行为，不导入 GPL 源码。
- Baseline C：Emby 官方命名规范覆盖率与 Emby.Naming 公开测试维度；不虚构闭源 Server 的准确率。
- Candidate：新引擎每个阶段的增量版本。

相同输入集必须冻结，报告需包含失败差集：新修复、旧回归、双方都失败、双方都错配。

## 首批必备类别

- 中文、英文、中英双标题、简繁、日文/韩文原名、罗马音。
- 数字标题与年份歧义：`1917`、`1984`、`3 Body Problem`、`Ming Dynasty in 1566`。
- 电影/剧集同名与重启年份。
- SxxExx、1x02、102、绝对集数、日期剧、多集范围、全季/全集。
- PT 规格、发布组、字幕组、流媒体平台、HQ/修复/未删减/导演剪辑。
- 单文件、49 集数字文件、Season 目录、BDMV、VIDEO_TS、多版本、extra/sample/trailer。
- 负样本：标题极短、只有发布规格、候选近分、年份冲突、类型冲突、恶意超长名称。

## 建议发布门槛

- 先冻结 corpus 与当前 baseline，再由数据决定阈值。
- 自动匹配以高精度为优先；近分和证据冲突应继续使用结构、年份、别名、译名和跨类型证据自动消歧。全部自动策略仍无法越过校准阈值时返回明确的未识别结果，不进入 Top-k 人工确认；Top-k 只作为内部召回指标。
- 每次新增规则必须同时带正样本和“合法标题不能被误删”的反例。
- 网络调用使用本地受控假 TMDB 或 cassette；实时 TMDB 仅用于手工验收，避免 CI 漂移。
