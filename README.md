# ViewHub

ViewHub 面向多个客户空间，通过 GB/T 28181 接入摄像头和录像机，提供实时监控、分屏、历史录像、云台控制及 G.711A 语音对讲。录像保存在现场设备，录像、云台与对讲取决于设备能力。

项目自有代码采用 MIT 许可证，允许商用、修改和再分发，须保留版权及许可声明。第三方依赖、字体和组件保留各自许可证；许可文本随源码及构建产物提供。FFmpeg 由设备端使用者自行安装，不随本项目分发。

React 管理台使用 shadcn/ui，按视频监控、设备管理和空间管理组织；实时画面与录像回放整合在同一通道工作区。实时监控支持单画面及 4、9、16 分屏，按账号保存分屏通道。浏览器优先用 WebRTC 播放，必要时切换到 WebCodecs 或 HLS；WebCodecs/HLS 模式的声音监听使用独立的只接收 WebRTC 音频连接，共用当前观看会话，无需麦克风；对讲通过另一条 WebRTC 音频连接与现场双向收发。播放器支持独立音量，以及可还原的亮度、对比度和饱和度显示调整。

- `apps/platform`：平台 Go 服务、React 管理台、国标信令、SQLite 存储、浏览器媒体输出及部署配置。
- `apps/capture`：Go + FFmpeg 的 USB 国标设备端及 React + shadcn/ui 本机配置页。
- `tests/integration`：平台和设备端的真实本地 socket 联合测试。
- `tests/architecture`：两端代码依赖边界检查。
- `scripts`：仓库统一验证入口。
- `docs/project`：架构、运行和验收文档。
- 根 `go.mod` 管理两端 Go 依赖；根 `compose.yaml` 保留平台部署入口。
