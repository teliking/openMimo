# 验证记录

时间：2026-10-25。环境：Windows / Go 1.27.1；代码要求 Go 1.24+。**没有连接相机；下面均不是相机端协议验收。**

## 自动验证

```powershell
go test -count=1 ./...
go vet ./...
go build -trimpath -o bin/mimo.exe ./cmd/mimo
```

最终结果：**82 个测试项通过（包含子测试），9 个有测试的包通过；其余 2 个主机包编译成功，`go vet ./...` 通过。** Android 专用 JNI 包另通过 APK 构建与真机执行验证。

测试包含：

- 公开 Kaze CRC、DUML、UDP 包头、路由、ACK、云台和 ISO 字节参考。
- 6 个从恢复后的 Java 方法独立执行得到的 JNI 值：4K60 格式、4K120/倍率、相对云台角、云台速度、轨迹点、相机枚举范围。Go 编码与参考逐字节一致。
- 所有 schema 的嵌套类型和枚举依赖可解析；缺字段、整数溢出、截断、超长列表、末尾多余字节、总列表预算被检查。
- 流分片、损坏帧后的重同步、10 位帧长边界。fuzz 的种子会随普通测试运行；没有进行长时间 fuzz campaign。
- 200 个并发请求应答匹配、不同 session 和错误命令的回复过滤、事件背压、动作超时不重放、关闭唤醒等待者。
- 独立接收窗口初始化、乱序与 16 位回绕、拒绝确认尚未发送的序号。
- 活动云台关闭时向模拟相机发送 joystick 归零及 speed 归零。
- 模拟 GATT 的已配对、相机端确认、拒绝、取消、先订阅后写入、FFF5 分片、通知重组、凭据查询及密码输出脱敏。
- HG212 原生 setter 与旧模块的编码差异；39 条原生分辨率映射、非恒等项及帧率 lookup mask；V2 压缩字段掩码和能力表边界。
- 4 组额外的公开属性推送参考字节及其预期字段：录像、白平衡、曝光/有效 ISO、对焦。
- 30 个并发属性读取，包含同名请求、过期 transaction、错名推送、加密推送、取消与关闭。
- 传输失败时保留底层错误，已发送动作返回结果不确定；拒绝不支持的加密发送与溢出的 BLE 定时器配置。
- 应用层动作校验、脚本全计划预校验、失败不重试、取消后的动作抑制、250 ms 云台看门狗、GATT 回调匹配与关闭、JSON 并发状态序列化、断线后的能力缓存清理。

完整 test JSON 在本工作目录 `analysis/go-test.jsonl`。未运行 race detector：当前默认 CGO 关闭且没有配置 Windows C 编译器。并发模拟测试不能替代 race detector，也不能替代长时间实机压力测试。

构建产物的 SHA-256 与环境摘要记录在 `analysis/validation-summary.json`。

## Android 真机的离线执行

构建目标 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`，作为 Android shell 下运行的静态程序。这个选择不提供 Android app 权限集成。

```powershell
adb -s kf6x7l85cq6tpflv push bin/mimo-linux-arm64 /data/local/tmp/openmimo-go
adb -s kf6x7l85cq6tpflv shell chmod 755 /data/local/tmp/openmimo-go
adb -s kf6x7l85cq6tpflv shell /data/local/tmp/openmimo-go status
adb -s kf6x7l85cq6tpflv shell /data/local/tmp/openmimo-go frame-decode 550f04a202f02b8040002b04002440
```

已成功执行：status 返回 HG212、1,374 layouts、897 enums、2,903 keys、321 native command templates、hardware_validation=pending。参考帧解析得到 sender=2、receiver=240、sequence=32811、set=0、id=43、payload=0400。

本轮增量构建同时验证：`video-format --resolution 98 --fps 7 --speed 4 --sdk-codes` 产生 `6507000400`；`property-decode cam_video_param_v2 10060000000201000101` 正确解析 resolution=16、frameRate=6、compression=1。此项仍是 Android 上的离线协议编码/解析验证。

没有向未连接的相机发送测试动作，没有安装修改版 Mimo。手机已安装独立的 `dev.openmimo` Android 控制端，保留专用分析 helper 与 Go 测试程序。

## Android APK 验证

产物 `bin/openmimo-0.1.0-arm64-debug.apk`，Android 10+ / arm64，已独立开发签名校验、安装并冷启动。真机 instrumentation 通过 **25 项断言**，包括 6 项 JNI 协议检查、UTF-8、五页面控件、离线按钮禁用、所选 Wi-Fi 的 NDK socket 绑定（未发包）和 connected-device 前台服务。输出保存在 `analysis/android-smoke.txt`；哈希、版本和总检查摘要在 `analysis/android-build.json` / `analysis/android-validation.json`。

Android GATT、Wi-Fi 交接及拍摄/参数/云台/任务接口已经接线。2026-10-25 已实测 Pocket 3 配对、MTU、热点交接、控制会话、状态和录像参数读回；用户确认云台动作与归中正常。其余项目分别验收，详见 [实机记录](HARDWARE.md)。框架和使用方式见 [Android 说明](../android/README.md)。

## 接 Pocket 3 后的验收顺序

1. 记录相机固件、网络拓扑，确认工具可达相机。先用 `monitor --duration 20s --verbose` 核验会话存活及状态推送，保存输出；参数初值留档。
2. 验证属性订阅、transaction 回显、模式与格式读回、能力表、V2 倍率标志和应答状态码。按单项、单次改变后立即读回的方式验证，恢复初值。
3. 录像开始/停止及拍照，同时观察相机屏幕与推送；超时先检查实际状态，不能盲目重发。
4. 云台先归中、小幅速度、归零，再验证绝对角、相对角换算、忽略轴及 duration。断线停止/固件超时单独测试。
5. 模式/分辨率/帧率组合、曝光、延时、轨迹点等分别测量，建立 **目标固件** 能力矩阵。最后再做丢包、重连和长时间录制。

待取得这些证据后，才能逐项将“实现了编码”提升为“相机已验证支持”。媒体预览、文件管理和加密变体另行验收。
