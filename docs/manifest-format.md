# 包格式与清单（.pak / MTPAK）

`tap` 生成的资源包是单文件、自描述、逐字节确定的容器。文件名为任意（习惯用
`.pak`），格式与文件名无关。

## 1. 物理布局

所有整数均为**小端序**。

| 偏移 | 长度 | 内容 |
|------|------|------|
| 0 | 5 | 魔数 `MTPAK` |
| 5 | 1 | 包格式版本 `uint8`（当前为 `1`） |
| 6 | 1 | 保留，必须为 `0` |
| 8 | 2 | 清单格式版本 `uint16`（当前为 `1`） |
| 12 | 4 | 算法版本 `uint32`（对应 `config.AlgorithmVersion`） |
| 16 | 4 | 清单 JSON 字节长度 `uint32` |
| 20 | 32 | 清单 JSON 的 SHA-256 |
| 52 | 32 | 整文件 SHA-256（计算时该 32 字节位置按零计） |

固定头共 **84 字节**，其后依次为：

1. **清单 JSON**（长度由头给出，规范编码，见下）；
2. **blob 表**：`uint32` 数量，随后每条目
   `[32]byte payload SHA-256` + `uint64 payload 字节长度`；
3. **payload 区**：各 blob 负载按清单中 `blobs` 数组顺序紧密排列。

加载时依次校验：魔数 → 整文件摘要 → 清单摘要 → 每条 payload 摘要与长度 →
尾部零余量。任何一步失败都拒绝整包，绝不会返回部分内容。

## 2. 版本兼容

- 包版本、清单版本、算法版本三者独立写入头部与 JSON，互相印证
  （头部与 JSON 不一致即判定损坏）。
- 加载方调用 `manifest.CheckCompatibility(m, readerMaxVersion)`：
  当 `MinSupportedVersion(1) <= m.FormatVersion <= readerMaxVersion` 才可读。
- 新版本构建器**不会**就地改写旧包语义：每个包永久携带其版本；旧加载器遇到
  更新的版本会收到 `ErrUnsupportedVersion`，错误信息区分“过旧”与“过新”。
- 修改清单/打包语义时必须提升 `FormatVersion`；修改压缩或处理语义时提升
  `config.AlgorithmVersion`（同时参见缓存文档）。

## 3. 清单 JSON

清单是**规范 JSON**：字段按结构体声明固定顺序输出，关闭 HTML 转义，紧凑编码，
以单个换行结尾；不序列化任何 map，因此 Go 版本、机器、遍历顺序都不会影响字节。

顶层字段：

```json
{
  "format_version": 1,
  "algorithm_version": 1,
  "codec": "flate-v1",
  "blobs": [
    {"id": "<hex sha256>", "codec": "flate-v1",
     "size": 1234, "raw_size": 5678, "hash": "<hex sha256>"}
  ],
  "assets": [
    {"path": "ui/button.png", "type": "png",
     "blob": "<hex sha256>", "size": 1234, "raw_hash": "<hex sha256>",
     "refs": [
       {"kind": "dependency", "path": "shared/noise.png",
        "target_blob": "<hex sha256>"},
       {"kind": "link", "path": "shared/noise.png",
        "target_blob": "<hex sha256>"}
     ]}
  ]
}
```

排序与稳定性约定：

- `blobs` 按 `id`（编码后内容的 SHA-256）升序；
- `assets` 按 `path`（源相对、POSIX 斜杠逻辑路径）升序；
- 单个资产的 `refs` 稳定排序后输出。

### 3.1 去重与 blob id

`blob.id == blob.hash == 编码后 payload 的 SHA-256`。内容相同的两份资源（无论
文件名、目录、创建顺序如何）映射到同一 blob，包内只存一份。改名/移动文件不产生
重复数据。

### 3.2 引用重写

源资源之间的引用由**侧车文件**声明，侧车永不入包：

- `<name>.refs.json`：给同名真实资源附加引用；
- 内容为 `{"kind":"refs","refs":[{"kind":"dependency","path":"../x.png"}]}`，
  或 `{"kind":"link","target":"../x.png"}`。

构建时：

1. 引用路径相对于侧车所在目录解析，禁止绝对路径与 `..` 越出源树；
2. 所有引用目标必须存在（严格模式直接报错；宽松模式记诊断并跳过该资产）；
3. `path` 解析为最终逻辑路径，`target_blob` 解析为目标内容最终落盘的 blob id；
4. `link` 定义一个**虚拟资产**（不需要同名图片文件），自身不存 payload，
   `type="link"`，直接别名目标的 blob；`link` 与真实文件同名是错误。

### 3.3 负载编码

默认 codec 标识 `flate-v1`：4 字节帧头 `FTX1` 后接固定级别 zlib 流
（内含 Adler-32 校验）。同输入同级别在任意机器/并行度下输出逐字节一致；解码时
校验帧魔数与 zlib 校验和，损坏返回 `codec.ErrCorruptPayload`。

## 4. 识别与坏输入

格式识别只看内容魔数（PNG/JPEG/二进制 PPM P6），不信任扩展名。识别后进行**完整
解码验证**（PPM 还严格校验头部与栅格长度）。失败错误为
`detect.StageError{Path, Stage, Err}`，其中 `Stage` 为 `identify` 或 `decode`，
可定位到具体资源；哨兵错误为 `ErrUnknownFormat` / `ErrCorruptInput`，可用
`errors.Is` 区分类别。
