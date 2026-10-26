# 协议分层和证据

目标是 Pocket 3 / HG212。这里的“APK 确认”指编码或调用链已经从指定版本定位；“公开资料”指上游报告过抓包或实测。两者都不等于本项目已经接相机验证。

## 数据经过的层

```text
拍摄流程 / 云台脚本（后续）
    ↓ 参数校验、状态读回、动作编排
pocket3.Command / telemetry
    ↓ DUML v1
BLE FFF4/FFF5                     TCP/7001 → UDP/9004 session
配对、AP 唤醒、凭据               路由、ACK、保活、控制、推送
```

`sdkvalue` 另外描述 **Java → JNI** 的值 ABI：例如 `VideoResolutionFrameRate` 包含两个小端 int32。`02/18` 的相机 payload 是五字节结构。这两个层没有通用的一对一直传关系，必须逐项恢复原生转换逻辑。

## DUML v1

| 偏移 | 长度 | 内容 |
|---|---:|---|
| 0 | 1 | `55` |
| 1 | 2 | 小端：总长度低 10 位，版本高 6 位；当前版本 1 |
| 3 | 1 | 前 3 字节的 CRC8 |
| 4 | 1 | sender，低 5 位组件、高 3 位索引 |
| 5 | 1 | receiver，同上 |
| 6 | 2 | 小端 DUML sequence |
| 8 | 1 | bit 7 应答，bits 5–6 ACK 类型，bits 0–2 加密标记 |
| 9 | 1 | command set |
| 10 | 1 | command ID |
| 11 | 可变 | payload |
| 末尾 | 2 | 除末尾两字节之外的 CRC16，小端 |

CRC8 初值 `77`、反射多项式 `8C`；CRC16 初值 `3692`、反射多项式 `8408`。允许总长度 13–1023。公开唤醒向量：`550f04a202f02b8040002b04002440`。

当前可以保留 flags，但没有加密/解密实现；不同版本明确拒绝解析。`Stream` 用于有序 BLE/TCP 字节流，`Scan` 用于完整数据报中的 DUML 发现；它不实现跨 UDP 丢包的可靠重组。

## UDP 数据链路

八字节包头：`length|8000:u16`、`session:u16`、`sequence:u16`、`type:u8`、前七字节 XOR。长度占低 14 位，均为小端。已处理类型：握手 0、窗口状态 1、媒体 2、数据 3、ACK 4、命令 5。

命令包内的 12 字节路由头为 `peerAck:u16 / txSequence:u16 / 00×4 / counter:u8 / 01 / class:u8 / 00`。普通命令的传输序号按 8 递增。26 字节 ACK 依次包含媒体接收窗口、数据接收窗口、本端发送窗口。第三组第一字段是相机确认本端发送的 cursor，不能拿本端下一次待发送序号代替。

初次看到流时接收其序号，随后使用 16 位回绕比较更新。状态包只为尚未见到直接数据的流初始化窗口。不会因收到一个状态包就覆盖已经接收到的流 cursor。尚未实现完整选择确认、缺口跟踪和重传，真实丢包行为待接机测量。

`session` 使用单个状态循环管理序号、写入、等待者和 ACK。应答必须匹配 DUML 序号、源/目的地址和命令集/号，UDP session 不同则丢弃。事件缓冲满时丢弃事件并计数，不阻塞应答。动作不会因等待超时自动重发；`ErrOutcomeUnknown` 保留结果不确定的含义。

`ReadProperty` 先注册等待者再发送订阅，按设备地址、属性名和 transaction 匹配。并发读取不抢占 `Events`，过期 transaction 不满足新请求。实机 HG212 的推送计数未回显请求 transaction，严格读取会超时；另提供 `ObserveProperty`，明确等待订阅发送后收到的下一条同名推送，不从缓存取值，也不声称请求关联。Android 的 `observe-property` 返回 `source=live-push`、`request_correlated=false` 和接收时间。断线时，已发送未确认的动作保留 `ErrOutcomeUnknown`，并能通过 `Client.Err()` 和 error chain 查看底层网络原因。

加密 DUML 不参与应答和属性匹配，也不做明文遥测解析；发送端明确拒绝尚未实现的加密标记。

## BLE 引导

服务 `FFF0`、通知/配对启动 `FFF4`、写入 `FFF5`，完整 UUID 见 `bootstrap.go`。流程为订阅通知、向 FFF4 写 `01 00`、`00/2B` 会话唤醒、`07/45` 配对、必要时处理相机端 `07/46` 确认并回送原始 payload，然后 `53/10` 唤醒 AP、`07/07` 查 SSID、`07/0E` 查密码。字符串为一字节 UTF-8 长度加内容。

`ble.Link` 是平台边界。Android APK 已实现 GATT 连接、按操作申请权限、扫描和 Wi-Fi 入网：`mobilecore.GATTLink` 把操作交给原生 Android 队列，收到回调后按 ID 确认；link ID 隔离旧连接。FFF5 默认每次写 20 字节。平台基础与离线行为已验证，真实相机的特征、蓝牙时序和凭据交接仍需接机核验，详见 [Android 框架](../android/README.md)。

## 控制命令覆盖

| 命令 | Go 接口 / 内容 | 主要依据 |
|---|---|---|
| `02/02` | `Record`，`01` 开始 / `00` 停止 | APK 原生动作函数 |
| `02/E1` | `SetMode`，8 项已映射模式 | APK quick-mode 编码 + 公开 Pocket 3 模式值 |
| `02/18` | `VideoSettings`，HG212 为 `[res fps 00 ratio 00]`；`VideoFormat` 提供旧模块保留位接口 | HG212 注册的 topic setter 与旧模块为不同路径 |
| `02/8E` | `GetProperty` / `SetProperty`；Pro、ISO MAX、Showcase、音频、轨迹参数 | 原生 GET + 公开 TLV/字段映射 |
| `02/01` | 拍照 `01` / 全景触发 `07` | 公开 Pocket 3 抓包资料 |
| `02/AB,1E,2A,28,2E,2C,42,24` | 编码、曝光、ISO、快门、EV、白平衡、色彩、对焦 | 公开 Pocket 3 设置资料；ISO 有独立向量 |
| `02/12,16,4A` | 照片画幅、RAW、倒计时 | 同上 |
| `02/6C,6E,E7` | 延时、移动延时、轨迹点、全景类型/格式 | 同上；轨迹 wire 轴顺序 yaw/roll/pitch |
| `04/01` | `Joystick` / `Neutral` | 公开操控向量，flags=0 |
| `04/4C` | `Recenter`，`FE 08` | 公开资料 |
| `04/14` | `AnglePayload`，8 字节角度编码 | HG212 原生 GetRotateAnglePack |
| `04/0C` | `SpeedPayload`，8 字节速度编码 | HG212 原生 RotateBySpeedAction |
| `00/99` | `SubscribeProperty` / `ParsePropertyPush` / `DecodeProperty`，10 类已解码属性 | 公开属性协议 + APK V2/能力表回调 |
| `04/05,02/80,0D/02,02/DC` | 云台、相机、容量、电池等状态解析 | 公开遥测资料 |

`CheckAccepted` 只适用于已确认“首字节 0 为成功”的应答，不能通用应用到任意命令，也不代表动作已完成。

### 录像参数的保留规则

旧 `CameraVideoModule::SetVideoResolutionFrameRateAndSpeedRatio` 使用缓存的五字节格式：修改 byte 0/1 以及 byte 3 的低六位，保留 byte 2、byte 3 高两位和 byte 4。`VideoFormat.WithFormat` / `WithSpeedRatio` 保留这条低层操作接口。

本轮进一步确认 `HG212CameraAbs::WillSetup` 注册的是 `VideoResolutionFrameRateAndSpeedRatioSet`（`0x1c2068c`）。该函数明确将五字节清零后写入分辨率、帧率、倍率低六位，产生 `[res fps 00 ratio 00]`。`VideoSettings.Command()` 复现这条 HG212 路径；这些零来自原生初始化，不是用未知字段补零。

原生分辨率初始化表有 39 条插入（包含重复项），`98 → 101` 并非恒等映射。`extract_resolution_mapping.py` 读取常量表，并通过 Capstone 核验 39 次插入的偏移与顺序，再生成 Go 转换表。帧率转换也按原生 lookup mask 处理：wire 0 转 SDK `FFFF`，wire 21/22 保持数值，并非简单照搬查表中的保留项。

`cam_video_param_v2` 当前解析 resolution byte 0、frameRate byte 1、byte 3 的低一位标志和 byte 8 的低四位压缩格式，其余原样保留。不能从一位标志还原任意倍率，也不能把推送前五字节直接复制到 `02/18`。

`camcap_video_format` 由原生 topic 构造函数确认名称：byte 0 控制有效性，byte 3 为条目数，随后每条三字节，前两字节为 resolution / fps，第三字节保留为 `RawOptions`。`CheckFormat` 只检查这两个值在表中，不把未知 options 或当前模式限制解释成支持任意倍率。

其余属性解码覆盖画幅、色彩/白平衡、曝光/有效 ISO、对焦、照片设置、延时、全景与轨迹状态。未知选择值保持数值，未知枚举不推断含义，原始复合字段保留。

### 云台坐标和时间

`AnglePayload` 按 yaw、roll、pitch 小端有符号 int16 写入，单位为 0.1 度；byte 6 是 absolute 与三个 ignore 位，byte 7 为原始 duration 字段。native packer 对持续时间存在先转整数再乘 10 的行为，故 API 使用 `DurationTicks` 保留原始值，没有把任意浮点秒数伪装成已验证接口。

原生 HG212 外层还会根据当前姿态与 yaw/body heading 换算相对角、处理忽略轴及限位。当前只完成 packer，调用它不等于已经完整复现 SDK 的相对运动行为。`SpeedPayload` 的值是 SDK 速度数值乘 10 后的 int16，末尾 `81 00`。

正常关闭会话时，若检测到未归零的 joystick 或 speed，会尽力发送对应停止包。不能在断网、进程被杀或相机不接受时保证停止，必须在实机中验证超时行为。

## 尚未完成

目标固件能力验收、完整 V2 属性解码/映射、全部原生命令的请求应答结构、Android 无线链路实测、其他系统 GATT 适配、可靠丢包处理、视频媒体重组与解码、文件枚举/下载/播放、加密与扩展版本及重连。有限拍摄编排已通过 `sequencer` 接入 APK；更复杂模式仍需逐项实现和验证。索引中保留未知项用于继续分析，不把其他机型命令标成 Pocket 3 支持。
