# 配置项

构建的输入分为两类：**影响输出字节的配置**（`config.Config`，进入缓存指纹）与
**仅影响运行方式的选项**（`config.Options`，永不进入产物）。

## 1. Config（影响产物）

| 字段 | CLI | 取值 | 说明 |
|------|-----|------|------|
| `Codec` | （当前固定） | `flate` | 压缩编解码器；名称变化使缓存全部失效 |
| `CompressionLevel` | `-level` | `0`（默认 6）或 `1..9` | 压缩级别；级别变化使缓存全部失效 |
| `OnError` | `-lenient` | `strict`（默认）/ `lenient` | 坏资源处理策略 |

- `strict`：任一资源识别/解码/编码/引用解析失败即整体失败，不写包；
- `lenient`：坏资源被跳过，构建成功，失败进入 `Report.Diagnostics` 与
  `failed` 状态，**诊断信息绝不写入包内**，因此不会破坏确定性；引用了坏资源的
  其他资源也会连带跳过（link 目标不可用时该 link 标记失败）。

注意：`OnError` 当前**不**纳入缓存指纹，因为它不改变成功资源的编码字节，只改变
成功/失败的集合；严格与宽松模式对同一组健康资源产出的包完全相同。

## 2. Options（仅运营，不影响产物）

| 字段 | CLI | 默认 | 说明 |
|------|-----|------|------|
| `Jobs` | `-jobs` | `GOMAXPROCS` | 最大并发处理资源数 |
| `CacheDir` | `-cache` | 空（禁用持久缓存） | 缓存根目录 |
| `MaxCacheBytes` | `-max-cache` | `0`（不限） | 缓存容量上限 |
| `PerAssetTimeoutMS` | `-timeout-ms` | `0`（不限） | 单资源处理超时（毫秒） |

## 3. 并发、超时与取消

- 工作池带缓冲信号量限并发，上限即 `Jobs`；
- 每个资源在独立的带超时 context 中处理；zlib 编码分块进行并在块间响应
  取消/超时，单个超大资源不会卡死整批；
- 单资源失败只影响自身（严格模式才升级为整批失败），错误经 `detect.StageError`
  携带路径与阶段，超时可通过 `errors.Is(err, context.DeadlineExceeded)` 区分；
- 取消构建：立即停止派发并返回 context 错误，因包仅在全部成功后原子发布，
  目标路径上不存在半成品；缓存中可能多出已完成条目不影响正确性，下次继续复用
  （中断恢复天然成立）。

## 4. 支持的输入

- PNG、JPEG（魔数识别 + 完整 `image` 解码验证）；
- 二进制 PPM（P6，严格头/栅格校验）；
- `*.refs.json` 侧车（引用与 link 定义，见 `docs/manifest-format.md`）；
- 其他文件：严格模式报错，宽松模式跳过。

## 5. 程序化调用

```go
c, _ := cache.Open("/var/lib/tap-cache")          // 可长期复用、跨进程共享
p := pipeline.New(c)
report, err := p.Build(ctx, "assets", "game.pak", config.Default(), config.Options{
    Jobs: 8, PerAssetTimeoutMS: 30000,
})
for _, r := range report.Reports { // cached | processed | link | failed
    fmt.Println(r.Status, r.Path, r.Reason)
}
```
