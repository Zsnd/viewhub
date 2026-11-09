# ViewHub

ViewHub 是面向多个客户空间的摄像头与录像机管理平台，提供设备接入、监控墙、实时预览、按日期回放和云台控制。

| 接入方式 | 实时视频 | 历史录像 | 云台 |
| --- | --- | --- | --- |
| GB28181 | 云端接收国标视频，浏览器优先使用 WebRTC | 国标录像查询和回放，取决于设备能力 | 支持的通道可启用国标云台 |
| 轻边缘 | WebRTC P2P 优先，失败时经云端中继 | ONVIF Profile G，可配置海康 ISAPI 回退 | ONVIF |
| Frigate | WebRTC P2P 优先，失败时经云端中继 | Frigate 自带 API 查询和 HLS 回放 | 由已配置的 ONVIF 能力提供 |

监控墙提供 4、9、16 分屏，按账号保存通道分配。全屏显示画面，退出后保留分屏与通道。历史时间轴使用现场时区。

- `cmd/gateway` 和 `web` 是 Go 云端、SQLite 存储与 React 管理台。
- `cmd/edge` 和 `internal/edge` 是 Go 现场网关及设备适配。
- `internal/gb`、`internal/media`、`internal/frigaterelay` 处理国标信令、媒体和 Frigate 中继。
- `deploy` 存放 Compose 附加配置及部署样例。
