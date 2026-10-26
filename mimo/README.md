# openMimo / Pocket 3 协议研究

面向 DJI Osmo Pocket 3（Mimo 内部型号 **HG212**）的 Go 协议库与命令行工具。当前依据本目录 Mimo **2.12.1 / 10001997** APK、恢复的 Java/JNI 代码、原生 SDK 和有来源的公开测试向量实现。

**已连接 Pocket 3 完成 BLE 配对、热点交接、TCP/UDP 会话与状态读取，并由用户确认云台动作和归中正常；协议仍未完整实现。** 参数设置、能力表、其他模式及长时间运行需分别验收，详见 [实机记录](docs/HARDWARE.md)。

已有原生 Android 控制端：[APK](bin/openmimo-0.1.0-arm64-debug.apk) · [安装、功能与框架说明](android/README.md)。包名 `dev.openmimo`，Go 核心通过 JNI 接入，已在提供的手机完成离线运行验证。

## 已实现

| 包 | 内容 | 验证范围 |
|---|---|---|
| `protocol/duml` | DUML v1 编解码、CRC8/CRC16、地址与控制位、分片重组、报文扫描 | 公开字节向量、截断/损坏/长度边界、分片边界 |
| `protocol/datalink` | Pocket 3 UDP 包头、12 字节路由头、三组 ACK 窗口、序号回绕 | 公开字节向量、模拟会话 |
| `session` | TCP 引导、UDP 握手、APP 注册、保活、异步遥测、并发请求与应答匹配、属性读回 | 本地模拟相机；尚无相机实测 |
| `ble` / Android adapter | 配对、相机端确认、唤醒 AP、读取 SSID/密码；Android GATT 队列与 Wi-Fi 交接 | 状态机模拟测试；Android JNI/平台基础验证，待接相机 |
| `protocol/pocket3` | 录像、拍照、模式、录像格式、曝光、ISO、快门、白平衡、色彩、对焦、延时/轨迹/全景、云台、属性订阅、状态解析 | 部分原生代码确认，部分公开抓包资料；详见协议文档 |
| `protocol/sdkvalue` | **1,374** 个 Java/JNI 值布局、**897** 个枚举的通用编码器和解码器；**2,903** 个 SDK Key 声明 | 所有布局依赖可解析，6 个值与独立运行的 Java 方法逐字节比对 |
| `protocol/wirecatalog` | 原生模板恢复出的 **321** 项命令索引，保留地址与类型名 | 命令发现索引，不能视为 321 项已实现接口 |
| `control` / `sequencer` / `mobilecore` | 应用动作校验、有限拍摄计划、取消/异常停止、Go 应用状态、云台看门狗、GATT 平台桥接 | 主机测试 + Android 离线检查 |

SDK 值布局和命令索引是整个 Mimo SDK 的合集，**不是 Pocket 3 的功能清单**。Java/JNI 值的 `int32` / `double` 字节不能直接当成相机 DUML payload。

## 构建和离线使用

Go 1.24 或更新版本，Go 代码仅依赖标准库。

```powershell
go test ./...
go vet ./...
go build -o bin/mimo.exe ./cmd/mimo

./bin/mimo.exe status
./bin/mimo.exe catalog --limit 10 video_format
./bin/mimo.exe keys --limit 10 Gimbal
./bin/mimo.exe schema VideoResolutionFrameRate
./bin/mimo.exe frame-decode 550f04a202f02b8040002b04002440
./bin/mimo.exe video-format --resolution 16 --fps 6 --speed 0
./bin/mimo.exe property-decode cam_video_param_v2 10060000000201000101
```

Java/JNI 值编码示例，输入使用恢复出的字段名与枚举名：

```powershell
'{"resolution":"RESOLUTION_3840x2160","frameRate":"RATE_60FPS"}' |
  ./bin/mimo.exe sdk-encode VideoResolutionFrameRate -
# 1000000006000000（这是 JNI 值，不是 02/18 payload）
```

## 相机连接和测试入口

先让运行工具的电脑或手机加入 Pocket 3 的 Wi-Fi。当前工具不负责操作系统 Wi-Fi 入网；可先使用 Mimo 完成首次配对，再退出 Mimo 释放连接。`session.Dial` 默认使用 TCP `7001` 与 UDP `9004`，相机地址默认 `192.168.2.1`。`--skip-tcp` 仅用于对照实验。

```powershell
./bin/mimo.exe monitor --duration 20s --verbose
./bin/mimo.exe read-property --name cam_video_param_v2
./bin/mimo.exe read-property --name camcap_video_format
# 发送一次已知 PID 的读取命令，返回原始应答，不自动解释成功状态
./bin/mimo.exe request --set 2 --id 0x8e --payload 00010f00
```

`monitor` 会注册会话、初始化云台控制链路并订阅属性；不是被动抓包。它输出云台、相机、电池、存储和属性推送 JSON，已能解码 10 类属性，包括 V2 录像参数和格式能力表。未知字段保留为原始值，未知帧只在 verbose 模式输出元信息。

`read-property` 在发送订阅前安装等待者，按属性名、transaction 和设备地址匹配推送，不消费其他调用方的事件。实机 HG212 的推送计数不回显订阅 transaction，因此此严格接口会超时。Android 使用独立的 `ObserveProperty` 等待下一条同名实时推送，并标记 `source=live-push`、`request_correlated=false`；它不返回缓存。订阅可能持续到会话结束。

`request` 是原始协议实验入口，按指定接收者、命令集、命令号和 payload 发一次包。动作超时返回 `ErrOutcomeUnknown`：可能已执行，应读回状态后再决定是否重发。库不自动重放录像、拍照等动作。

提供的 Android 真机 `kf6x7l85cq6tpflv` 已完成原版 Mimo 初始化与分析。`bin/mimo-linux-arm64` 是用于 Android shell 的静态 Linux/arm64 构建；离线运行记录见 [验证记录](docs/VALIDATION.md)。它不是 Android APK，也不包含 Android 蓝牙权限或 Wi-Fi 管理界面。

## Go 调用

```go
ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()
c, err := session.Dial(ctx, session.Config{})
if err != nil { return err }
defer c.Close()

reply, err := c.Request(context.Background(), pocket3.Record(true))
if err != nil { return err }
// 02/02 使用首字节状态码。应继续通过 02/80 确认录像状态变化。
if err := pocket3.CheckAccepted(reply); err != nil { return err }
```

Pocket 3 专用写入使用 `VideoSettings.Command()`，与 `HG212CameraAbs` 注册的原生 setter 一致：`[resolution fps 00 ratio 00]`。`video-format` 只编码并输出 payload；实际发送仍使用 `request` 或 Go 会话接口。

`VideoFormat [5]byte` 保留旧 `CameraVideoModule` 的保留位操作接口，不再把它当作 HG212 唯一路径。`ParseVideoParametersV2` 独立解析推送，不能把推送前五字节直接当作写入参数。V2 的一位倍率标志不等于完整数值倍率，尚不提供从 V2 自动推导倍率的接口。

`SDKResolutionToWire` / `WireResolutionToSDK` 来自原生初始化表，包含 `98 → 101` 的非恒等映射；`SDKFrameRateToWire` / `WireFrameRateToSDK` 保留未知值规则。转换只说明编码，不说明相机支持。

Android 已实现 `ble.Link` 的平台桥接：先订阅 FFF4/FFF5，提供缓冲通知、上下文可取消的写入和可中断的关闭。`ble.Start` 负责其上的配对状态机；`Credentials.Password` 默认不会出现在日志 JSON 和 `String()` 中。APK 的内部凭据交接显式传给系统 Wi-Fi API，不传入界面事件日志。

## 后续完成条件

1. 接 Pocket 3，记录固件版本，核验握手、ACK 窗口、保活、属性订阅和状态布局。
2. 逐项验证参数：读取初值、只改一项、读回结果、恢复初值；核验 V2 倍率含义、能力表和属性 transaction 回显。
3. 实测云台速度、角度、参考坐标和限位；原生编码器已恢复，SDK 外层的坐标换算与限位尚未移植。
4. Android GATT/Wi-Fi 适配已接入 APK，接机验证；Windows 适配、重连、丢包和不同固件行为继续完善。
5. 继续分析尚未实现的媒体重组/预览、媒体文件管理、加密及扩展 DUML 变体。

新的拍摄流程可以在已有协议上编排；新分辨率或帧率能否支持取决于相机固件，不能由增加一个客户端枚举推出。

[协议与证据](docs/PROTOCOL.md) · [逆向记录](docs/RESEARCH.md) · [验证记录](docs/VALIDATION.md) · [第三方来源](THIRD_PARTY_NOTICES.md)
