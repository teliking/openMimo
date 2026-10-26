# Pocket 3 实机记录 · 2026-10-25

设备为用户提供的 Pocket 3，广播名 `OsmoPocket3-44AA`；控制手机 `kf6x7l85cq6tpflv`，Android 13。相机固件版本尚未取得，本记录不推断其他固件的行为。使用独立 `dev.openmimo` APK、Java Android 适配层和 Go 协议核心，原版 Mimo 已退出。

## 已取得的证据

| 项目 | 结果与边界 |
|---|---|
| BLE 扫描/服务发现 | 发现目标相机，FFF4/FFF5 通知和写入可用 |
| 配对 | 36 字符 UUID 返回单字节 `E3`；去掉连字符成为 32 字符后进入相机确认，用户确认后完成。随后连接返回已配对 |
| ATT MTU | 未协商时长通知只收到 20 字节，31/62/73 字节帧不能完整解析；请求 MTU 517 成功后完整接收并通过 CRC |
| AP 凭据 | `07/07` 与 `07/0E` 成功取得 SSID/密码，并交给 Android Wi-Fi API；日志未保存密码 |
| AP 唤醒 | `53/10` 的本机回复为 `E0`，但相机已经开机，后续凭据查询和 Wi-Fi 连接成功；不能据此验证关机唤醒 |
| Wi-Fi/控制会话 | 系统 Wi-Fi 请求成功，TCP 7001 引导、UDP 9004 握手、APP 注册和探测成功，链路持续健康，多次 APK 更新后可重新连接 |
| 状态 | 相机待机、普通录像模式，电池 100%；存储、云台姿态与录像状态推送可解析 |
| 录像参数读回 | `cam_video_param_v2=10060000000201000101`：resolution 16 / fps 6 / ratio flag 0 / compression 1，即 4K60、HEVC。仅证明读取到现值 |
| 属性推送 | 已取得录像、画幅、曝光、色彩、镜头、照片、延时、全景、轨迹延时 9 类属性。推送的 32 位计数独立递增，不回显订阅 transaction |
| 云台动作 | 用户在手机操作后明确确认“云台会动，归中正常”；日志有归中与绝对角命令应答。绝对坐标、角度精度、时长与限位未验收 |
| 录像命令 | 用户手机操作期间观察到 `record-start`、`record-stop` 应答成功；尚未核对实际生成文件，不能标记端到端录像验收通过 |

## 读取语义

`session.ReadProperty` 保留严格 transaction 匹配，用于兼容性研究。本机的独立推送计数使它超时。

新增 `session.ObserveProperty` 先注册等待者并发送订阅，再等待下一条来自 `0x28→0x02`、CRC 正确、未加密、同名属性推送。不返回调用前的缓存，不声称该值是请求的关联回复。Android 对应 `observe-property`，输出包含 `source=live-push`、`request_correlated=false` 与接收时间。

`camcap_video_format` 尚未取得，不能将界面预设组合当成相机实测能力表。参数设置、拍照、模式切换、自动任务、预览、文件管理和异常断线停止仍待独立测试。手机操作期间曾出现 `D9/DF` 应答，早期日志未包含动作名，无法可靠归因；新版错误已包含动作名和命令号。

最终 APK 的实际 `observe-property cam_video_param_v2` 在 `2026-10-25T08:01:51.690429072Z` 成功，收到计数 16475；`camcap_video_format` 同次读取明确超时。最后快照链路 healthy，Malformed=0、ForeignSession=0、Pending=0；DroppedEvents=93 为启动阶段未消费的有界事件队列丢弃，不据此宣称无丢包或已通过压力测试。

## 证据文件

- `analysis/pocket3-first-connection.log`：首次走通连接与用户操作期间的事件。
- `analysis/pocket3-live-diagnostics.json`：最后一次拉取的私有状态、遥测、实时读取结果。
- `analysis/pocket3-connection.log`：最后一次连接的事件快照。
- `analysis/android-build.json`：当前 APK 与 native 库哈希。
- `analysis/android-smoke.txt`：当前 APK 在手机上通过 25 项离线断言。
- `analysis/go-test.jsonl`：82 个测试项（包含子测试），9 个有测试的包通过；`go vet ./...` 通过。

诊断文件是本地开发数据；`files/diagnostics.json` 每秒覆盖写入一份快照，`files/connection.log` 有界保留最近 180 条。都不包含相机 Wi-Fi 密码。当前 `hardware_validation=pending` 表示完整功能矩阵仍未验收，不能用连接成功替代所有功能验证。
