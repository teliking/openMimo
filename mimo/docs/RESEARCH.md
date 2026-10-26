# 逆向分析记录

## 输入和定位

- APK：`1790237230756-DJI-Mimo-store-official-release-10001997_sec.apk`
- SHA-256：`877cce18fb21cbdb6e025965e7eab4b06d823675428f61b14f641b719454c2a2`
- 包名 `dji.mimo`，versionName `2.12.1`，versionCode `10001997`，arm64-v8a。
- Pocket 3 由产品资源与 SDK 分支定位为 **HG212**，原生 ProductType 值 145。不能用 HG214/HG224 分支替代。
- 真机：`kf6x7l85cq6tpflv`，M2012K10C，Android 13，已有 root；所有 ADB 操作明确指定此序列号。

## AppGuard 与运行时 DEX

APK 外层入口为 AppGuard，直接 JADX 只有少量壳类，真实 Application 是 `dji.mimo.main.DJIApplication`。本次使用已授权的手机运行原版 Mimo，外部 helper 读取目标进程映射与内存，没有修改 APK 安装内容。

在专用 Frida server 运行时 Mimo 反复很快退出，停止该 server 后应用恢复稳定；当前按反插桩干扰处理，使用外部读取方案。没有开启手机上被禁用的全局日志服务。

恢复出 41 个应用 DEX、151,789 个应用类；包含系统运行时等输入后索引为 48 个唯一 DEX、155,999 类。部分方法体落在原声明 DEX 末尾之外、仍处于外层映射中。`repair_dex_extent.py` 在分析副本扩大可读取范围并重算 header 校验，原始 dump 保留。

SDK 提取器从实际 `ByteStreamHelper` reader 调用恢复字段顺序、宽度、枚举、嵌套值和数组。结果是 1,374 layouts、897 enums、2,903 keys；自动提取没有未解析字段和未解析依赖。这个统计只说明提取器覆盖了识别到的结构，不是所有结构的逐项语义验证。

## 原生 ELF 恢复

`libdjisdk_jni.so` 的主要代码并未整体加密。保护层破坏了 section 元数据和 `PT_DYNAMIC` 的文件偏移；通过 `PT_LOAD` 的虚拟地址/文件偏移关系重定位 dynamic 数据，在副本上清理不可信 section header，恢复 41,970 个符号。

脚本：`scripts/restore_elf_metadata.py`；分析输入：`analysis/libdjisdk_jni-static-restored.so`；IDA 数据库已保存为 `analysis/libdjisdk_jni-static-restored.i64`。以下均为该 ELF 的 IDA 地址，不是手机加载后的 ASLR 地址。

| 地址 | 函数 / 结论 | 保存证据 |
|---|---|---|
| `0x260c944` | `DjiProtocolEncoder::Encode`：DUML v1 长度与 CRC；同时存在未移植的扩展/加密分支 | `analysis/native/duml-encoder.json` |
| `0x16cd804` | `CameraAbstraction::SetQuickCameraMode` → `02/E1` | `quick-mode.json` |
| `0x1775148` | `CameraVideoModule::ActionStartRecordVideo` → `02/02 01` | `start-record.json` |
| `0x1775800` | `ActionStopRecordVideo` → `02/02 00` | `stop-record.json` |
| `0x1776ee0` | `SetVideoResolutionFrameRateAndSpeedRatio` → `02/18`，五字节、保留未知位 | `video-format.json` |
| `0x1df58bc` | `HG212CameraAbs::WillSetup`，`0x1df715c` 引用专用录像格式 setter | `video-hg212-registration.json`；完整函数反编译被工具截断 |
| `0x1c2068c` | HG212 注册的 `VideoResolutionFrameRateAndSpeedRatioSet`，五字节零初始化后填入 res/fps/ratio | `video-format-topic-set.json` |
| `0x1c1f3d4` | HG212 注册的 `VideoResolutionFrameRateSet`，设置 res/fps，余字段清零 | `video-format-topic-set-simple.json` |
| `0x1c58a3c` | V2 topic 的倍率回调只取 byte 3 低一位 | `video-format-v2-ratio-topic.json` |
| `0x1c58bc4` | 视频格式能力回调：有效标记、count、三字节条目 | `video-format-range-topic.json` |
| `0x1cd49b0` | capability topic 名称 `camcap_video_format` | `video-capability-topic-constructor.json` |
| `0x14fb38c` | SDK/wire 分辨率初始化表；常量 `0xbdbe9c`，39 次 emplace；帧率表 `0xbdc054` | `protocol/pocket3/resolution-map.json`，含每次插入地址与偏移 |
| `0x177431c` | `OnCaptureParaPush`：从原生 capture 参数中缓存当前五字节格式 | `video-format-readback.json` |
| `0x16cabd0` | `SendOneTlvBufferGetPack` → `02/8E 00 01 PIDlo PIDhi` | 分析定位，尚未独立导出完整伪代码 |
| `0x2198e68` | `HG212GimbalAbs::WillSetup` 注册旋转动作 | IDA 调用链 |
| `0x21a523c` | HG212 `GetRotateAnglePack` → `04/14`，8 字节 | `gimbal-angle-pack.json` |
| `0x21a5380` | HG212 `RotateByAngleAction`，包含外层坐标/限位处理 | `gimbal-angle.json` |
| `0x21a5b54` | HG212 `RotateBySpeedAction` → `04/0C`，8 字节 | `gimbal-speed.json` |

`HG210` 的角度包变体为不同布局，不能套用到 HG212。`HG212::CameraCharacteristics` / `GimbalCharacteristics` 是很大的函数；工具未给出完整反编译。`hg212-gimbal-characteristics-excerpt.txt` 明确只是截断片段，不能当完整能力表。

## 可复核的提取工具

| 脚本 | 用途 |
|---|---|
| `inventory_apk.py` | APK 摘要、ZIP 清单、目标文件提取 |
| `read_process_dex.c` / `capture_stopped.py` | 外部读取运行时 DEX；后者会暂挂并恢复目标 Mimo 进程 |
| `index_dex.py` / `repair_dex_extent.py` | 类索引和方法体范围修复 |
| `decompile.ps1` | 使用本地 JADX、正确传入 checksum 选项 |
| `product_resources.py` | aapt 产品资源交叉定位 |
| `read_process_module.c` / `restore_elf_metadata.py` | 原生模块读取与静态 ELF 元数据恢复 |
| `extract_sdk_schema.py` | 从修复后 Java 源码生成 JNI 值 schema 和 Key 索引 |
| `extract_wire_catalog.py` | 从原生模板提取命令集/号/类型/地址 |
| `extract_resolution_mapping.py` | 从 ELF 提取转换表，用 Capstone 核验初始化插入次序，再生成 Go 转换数组 |
| `java_reference_vectors.py` | 独立运行恢复出的 Java 方法，生成 6 个编码参考值 |

部分脚本固定了本工作站 JDK 路径及已授权设备序列号；其他环境需先调整。恢复目录和本地工具不随普通 Go 构建分发。`go test ./...` 不需要 APK、手机、JADX 或 IDA。

Java 参考适配器保留 `fromBytes/toBytes/serializedLength` 方法体，移除 Android Parcelable/JSON 依赖，替换已知资源常量，并修复一处 JADX 的 bool→byte 表达错误。Go 测试读取其结果，因此不是拿 Go 自己生成的值做循环自证；但 6 个参考值不能代表 1,374 个类型全部经过独立测试。

公开项目的引用版本、复制的测试向量和许可证见 [第三方来源](../THIRD_PARTY_NOTICES.md)。公开资料只补充了本 APK 尚未逐项恢复的字段，未将上游实机结论写成本项目实机结论。
