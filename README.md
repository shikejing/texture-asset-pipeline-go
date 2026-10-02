# texture-asset-pipeline-go

纹理资源压缩与打包流水线：把源资源目录加工成运行时可加载的确定性资源包
（`.pak`），并维护可复用、可自愈的内容寻址构建缓存。

## 快速开始

```bash
go build ./cmd/tap
./tap -src ./assets -out ./game.pak -cache ./.tap-cache -jobs 8
./tap -src ./assets -out ./game.pak -cache ./.tap-cache   # 增量：仅重处理变化资源
```

输出示例：

```
processed    ui/button.png
processed    shared/noise.png
link         ui/button-alias.png
```

状态取值：`processed`（重新编码）、`cached`（命中缓存）、`link`（别名虚拟资产）、
`failed`（仅 `-lenient`）。

## 关键性质

- **逐字节确定性**：产物只由输入内容 + `Config` 决定。目录遍历按名排序、
  并发结果按键归集后统一排序、blob 以内容哈希为 id、序列化固定字段顺序，
  重复构建 / 换机器 / 换并行度输出完全一致。
- **增量等价全量**：未变化内容命中缓存，变化内容重编码；增量包与干净重建包
  逐字节相同（测试 `TestIncrementalBuildMatchesCleanBuild`）。
- **内容去重 + 引用重写**：相同内容包内只存一份；引用经侧车声明，构建期解析到
  最终 blob 位置；改名/移动不产生重复数据。
- **并发可控、失败可区分、取消无半成品**：`-jobs` 限并发，`-timeout-ms` 限单
  资源耗时，严格/宽松两种失败策略；包先内存组装后原子发布，失败/取消不留半截。
- **缓存可靠**：键只含内容哈希与配置指纹；版本戳不匹配整体失效；条目带长度与
  双重 SHA-256，损坏即剔除并重做，临时文件永不被当作结果。
- **坏输入容错**：魔数识别 + 完整解码验证，错误携带资源路径与阶段；坏资源不会
  进入清单，损坏包无法通过完整性校验。
- **版本标记**：包版本、清单版本、算法版本独立写入并互相印证，加载方可用
  `manifest.CheckCompatibility` 明确判定可读性。

## 包结构

| 包 | 职责 |
|----|------|
| `config` | 输出相关配置与运营选项、校验、算法版本常量 |
| `manifest` | 规范 JSON 清单、类型定义、版本兼容判定 |
| `detect` | 内容魔数识别（PNG/JPEG/PPM）与完整解码验证 |
| `codec` | 确定性 zlib（`flate-v1`）编解码与帧校验 |
| `cache` | 内容寻址缓存、配置指纹、完整性校验、原子写、LRU 淘汰 |
| `pack` | MTPAK 包格式：固定头、blob 表、原子写、加载校验 |
| `pipeline` | 扫描、引用解析、并发处理、去重、清单组装、发布 |
| `cmd/tap` | 命令行入口 |

## 文档

- [docs/manifest-format.md](docs/manifest-format.md)：包物理格式、版本规则、清单 JSON、引用/link 语义
- [docs/cache-semantics.md](docs/cache-semantics.md)：缓存键、失效、完整性、原子性
- [docs/configuration.md](docs/configuration.md)：全部配置项与并发/失败行为

## 测试

```bash
go test ./...            # 单元 + 端到端
go test -race ./...      # 竞态检测
```

覆盖：增量/全量一致、跨并行度与跨目录确定性、创建顺序无关、内容去重与改名、
引用解析、严格/宽松失败、单资源超时、构建取消无残留、缓存位翻转/截断/临时文件/
版本戳失效、包损坏拒绝与版本兼容判定。
