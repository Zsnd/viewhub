# ViewHub Capture

把本机 USB 摄像头作为 GB/T 28181 设备接入 ViewHub。Go 管理采集与国标会话，FFmpeg 采集编码，React + shadcn/ui 提供本地配置页。

## 需求与验收

- 单路视频，Windows DirectShow、Linux V4L2；可选择 H.264 / H.265，通过 PS/RTP、UDP 接入。
- 配置页可选择分辨率、输出帧率、H.264/H.265、标准/高清/更高画质与最大码率。后台按配置给出上限建议，默认自动跟随；手动填写后切换视频配置也保留，点击「使用建议值」恢复自动。检测设备读取 Windows 摄像头实际 MJPEG 规格；输出帧率可以低于采集帧率。AV1 留作后续。
- 后台自动按 NVIDIA NVENC → Intel QSV → CPU 顺序实际试编码，无编码设备配置项或状态展示；具体选择仅记入诊断日志。
- 操作者选择摄像头与参数，填写平台 SIP 地址、域、平台/设备/通道 20 位编码及密码。
- 修改配置后可直接点击「保存并启动设备」；保存成功才启动，参数校验失败保留编辑内容并显示原因。也可单独点击「保存配置」。
- 保存不自动启动。明确点击启动后注册、周期刷新与心跳；收到合法 INVITE、ACK 后采集推流；BYE、停止或退出释放摄像头。
- 注册状态、媒体状态、发送帧数和错误分开显示；网络故障重试，采集进程退出显示原因。
- 校验有限整数、端口、帧率、尺寸与码率。密码不从 API 返回；修改配置时空密码保留旧值。
- 配置和凭据保存于系统用户私有数据目录；Web 默认仅监听回环地址，同源写请求，禁止直接暴露公网。
- 暂不实现音频、对讲、PTZ、录像回放或自动 Windows 服务。Linux/ARM64 可构建不等于已在树莓派实机验收。

验收顺序：配置错误拦截 → USB 枚举 → 注册/目录 → ViewHub 点播真实 USB 画面 → BYE 停流 → 再次点播 → 停止/重新注册。用独立本地 ViewHub 实例验收，不修改现有生产设备。

## 架构

`web → HTTP 控制接口 → 设备生命周期 → SIP 会话 / FFmpeg → Annex-B 帧 → PS 封装 → RTP/UDP → ViewHub`

- `config`：默认值、校验、原子持久化，运行时快照不可变。
- `device`：拥有 SIP socket、注册/心跳循环与单个媒体会话；停止等待资源释放，配置变更只能在停止时进行。
- `media`：FFmpeg 参数用 argv 传递，不经过 shell；强制 AUD、无 B 帧、重复 SPS/PPS 或 VPS/SPS/PPS。按 AVC/HEVC 访问单元封装 PS，正确声明码流类型，按 MTU 分片，统一 SSRC/90kHz 时间戳。
- `encoder`：分别生成 CPU、NVENC、QSV 的低延迟参数；点播时用用户设置的分辨率、帧率和码率编码 3 帧合成画面，验证驱动可用、AUD 帧边界和首帧参数集后才采集摄像头。每次点播重新自动检测，不把编译支持当作硬件可用。
- `HTTP`：状态/设备/配置读取、保存、启动和停止；仅回环监听，同源写操作校验，前端静态资源本地托管。
- React：源、平台、状态三块工作区，轮询只读状态；表单编辑不被轮询覆盖。

方案取舍：不使用带二进制国标 SDK 的桌面 Demo；不从头写 USB 驱动或 H.264 编码；Go 设备端配合 FFmpeg 使平台适配留在采集参数中。使用仓库现有 sipgo 版本，不改动平台实现。

## 开发

在 `apps/capture/web` 执行 `npm install`、`npm run build`。仓库根目录执行 `go run ./apps/capture/cmd/capture -web apps/capture/web/dist`，打开终端显示的本机地址。系统需安装 FFmpeg，或在配置页指定其可执行文件绝对路径。

Windows 可运行 `./apps/capture/build.ps1`，默认构建到 `%LOCALAPPDATA%/ViewHubCapture`。先停止已运行客户端，再构建；运行产物中的 `start.cmd` 后打开 `http://127.0.0.1:18491`。配置位于 `%APPDATA%/ViewHubCapture/config.json`，不要上传该文件。FFmpeg 不随本项目重新分发，分发其二进制时应核对对应构建的许可证。

Linux 使用对应架构的 Go 构建产物，配套复制前端 `dist` 为 `web`，执行 `./viewhub-capture -web ./web`，配置默认位于用户配置目录。摄像头账号需有 `/dev/video*` 访问权限，FFmpeg 需带 V4L2 支持。当前要求摄像头支持选定参数的 MJPEG 输出；其他输入像素格式需要后续适配。

CPU 使用 `libx264` / `libx265`，NVIDIA 使用 `h264_nvenc` / `hevc_nvenc`，Intel 使用 `h264_qsv` / `hevc_qsv`。硬件模式需对应 FFmpeg 构建、支持编码的显卡及驱动；Linux Intel 还需可访问的真实核显 DRM 设备和媒体驱动，虚拟机的 VMware SVGA 不提供 Intel QSV。Linux/Intel 实机编码尚待验证。CPU 使用 ultrafast/zerolatency，实际可持续分辨率和帧率取决于机器性能。Windows 根据设备能力选择实际采集帧率，再按选定帧率输出；Linux 尚未进行同等能力枚举与实机验证。

### 画质与码率控制

配置使用 `quality`（standard/high/ultra）、`maxBitrate`（128–20000 kbps）、`bitrateMode`（auto/manual）。旧私有配置的 `bitrate` 数值迁移为手动上限，默认高清，避免覆盖用户数值；再次保存只写新字段。此功能改变的是设备端编码控制，不改变平台媒体协议。

`video_profile.go` 是推荐值和档位映射的唯一实现。1080p30 高清的起点建议是 H.264 6000kbps、H.265 4000kbps，再按像素数、帧率和档位缩放并取整、限制范围。这是经验起点，不是实测带宽预测，也不保证不同硬件感知画质相同；画面运动、噪声、光照和编码器会影响实际用量。自动模式保存时服务端重新计算，前端预览通过只读推荐接口获取，避免两套公式漂移。

- CPU 使用 CRF，并设置最大码率及半秒 VBV 缓冲。
- NVIDIA 使用 VBR-CQ、`-b:v 0`、最大码率和半秒 VBV，保留无 B 帧、无前瞻的实时设置，使用 qres 多遍和 strict GOP 稳定码率。
- Intel 使用 QVBR：质量参数、低于最大值的参考码率及上限共同设置；不能用不受最大码率约束的纯 ICQ 冒充有上限。驱动不支持该组合时，实际试编码失败并回退 CPU。

三个画质档位在 H.264 的质量参数为 28/23/19，H.265 为 30/25/21；数值越低保留细节越多，达到上限时允许牺牲画质。码率上限是编码器缓冲窗口内的约束，不是网卡瞬时限速；启动关键帧、编码器容差、PS/RTP 与网络开销可使短时观测超出，不能据此保证每秒严格不超限。

参数依据：[NVIDIA FFmpeg 编码说明](https://docs.nvidia.com/video-technologies/video-codec-sdk/13.1/ffmpeg-with-nvidia-gpu/index.html)、[FFmpeg 8.0 QSV 码率模式选择实现](https://github.com/FFmpeg/FFmpeg/blob/n8.0/libavcodec/qsvenc.c)。

本机 MJPEG 输入为 JPEG 全范围。客户端显式执行 full → limited 转换，输出 `yuv420p`（QSV 为 `nv12`）并标记 `color_range=tv`。FFmpeg 仍可能提示 `deprecated pixel format used`，因为输入使用旧的 `yuvj422p` 名称；这条警告本身不等于采集或编码失败，不能用隐藏全部警告来处理它。

`go test ./apps/capture/...` 验证设备端配置、编码选择、SDP 和媒体边界；注册、目录及注销联合测试在仓库根运行 `go test ./tests/integration/...`；跨平台使用 `GOOS=linux GOARCH=arm64 go build ./apps/capture/cmd/capture`。默认不录制或保存摄像头画面。

可选真实编码测试：设置 `VIEWHUB_TEST_FFMPEG` 为 FFmpeg 路径、`VIEWHUB_TEST_ENCODERS=cpu,nvidia`（Intel 机器可改为 `cpu,intel`），运行 `go test ./apps/capture -run TestEncoderFFmpeg -v`。只生成内存中的合成画面，不打开摄像头或保存视频。旧私有配置中若存在试用版 `encoder` 字段，会忽略该字段并自动选择，下一次保存时移除。
