# ViewHub 部署与运维

## 云端启动

在仓库根目录执行，宿主需要 Docker Engine 与 Compose。生产环境使用 HTTPS，站点默认地址为 `https://viewhub.xliyun.com`。

1. 将 `.env.example` 复制为 `.env`。填写公网媒体地址 `PUBLIC_IP` 和 `VIEWHUB_MQTT_CLOUD_PASSWORD`。若更换域名，在 `compose.yaml` 中同步调整传入容器的 `VIEWHUB_PUBLIC_ORIGIN` 和 Caddy 站点地址。
2. 创建 `secrets/web.password`，内容为管理员初始密码。用 `mosquitto_passwd -c secrets/mosquitto.passwd cloud` 生成 MQTT 密码文件，输入与云端环境变量相同的密码。后续添加静态站点账号时省略 `-c`，以保留已有条目。
3. 仅使用自动登记时，`secrets/sites.json` 可以省略。静态登记时填入站点数组，每项包含 `id`、`name`、`space`、`token` 和 `provider`。其中 `space` 是目标客户空间 ID，`provider` 是 `edge` 或 `frigate`，`token` 至少 32 个字符。
4. 执行 `docker compose config --quiet` 和 `docker compose up -d --build`。按 `deploy/viewhub.caddy` 配置反向代理。HTTP 与 MQTT WebSocket 的本地端口保持绑定回环地址。
5. 请求 `/healthz`，核对状态和版本，再登录验证设备列表。健康检查只验证云端响应。设备在线、视频和录像需分别验证。

| 位置 | 默认端口 | 用途 |
| --- | --- | --- |
| 云端公网 | TCP 443、80 | HTTPS、MQTT 和媒体隧道共用 443，80 用于 HTTP 跳转或证书申请 |
| 云端公网 | TCP 和 UDP 5060 | GB28181 SIP |
| 云端公网 | UDP 10000–10099 | GB28181 RTP，按 `RTP_FIRST` 和 `RTP_LAST` 调整 |
| 云端公网 | UDP 8191 | 云端 WebRTC，按 `ICE_PORT` 调整 |
| 云端回环 | TCP 18481、18482 | 应用和 MQTT WebSocket 的反代上游 |
| 现场 | TCP 和 UDP 18556、UDP 18558 | 实时 WebRTC 与轻边缘录像回放 ICE，需能通过所选 ICE 候选访问 |

现场网关主动连云端，摄像头和录像机的管理、ONVIF 和 RTSP 端口留在局域网。公网 P2P 取决于现场和观看端的网络。

## 轻边缘和 Frigate

**自动登记轻边缘。** 运维在目标空间的已认证会话中向 `POST /api/edge` 提交 JSON，例如 `{"name":"仓库网关","lan":"192.168.1.0/24"}`。将返回的 `compose` 字段内容保存为现场机上的 `compose.yaml`。其中含站点凭据，按密钥文件保管。在 Linux `amd64` 或 `arm64` 和 Compose 2.23.1 或以上版本运行 `docker compose up -d --build`。云端提供边缘二进制和对应的 MQTT 账号。现场时钟需保持同步。

**手工配置已有站点。** 在仓库根目录，从 `deploy/edge.example.json` 和 `deploy/go2rtc.example.yaml` 分别创建 `deploy/channels.local.json` 和 `deploy/go2rtc.local.yaml`。将 `VIEWHUB_ID`、`VIEWHUB_EDGE_TOKEN`、`VIEWHUB_MQTT_EDGE_PASSWORD` 与云端登记匹配，`VIEWHUB_MEDIA_URL` 使用对应站点 ID。Web 管理的 ONVIF 接入需要 `VIEWHUB_ONVIF_LAN` 覆盖设备所在网段。执行 `docker compose -f compose.edge.yaml up -d --build`。

**已有 Frigate。** 使用 `deploy/channels.frigate.example.json` 的通道结构，`frigate` 填实际摄像头键名。设置 `VIEWHUB_FRIGATE=http://frigate:5000`，确认 Frigate 的 Docker 网络与附加文件中配置的 `frigate_default` 一致，再执行：

```sh
docker compose -f compose.edge.yaml -f deploy/frigate.compose.yaml up -d --build
```

站点 go2rtc 的流名要与通道的 `stream` 匹配。Frigate 继续管理其本地录像和录像保留期限。

## 升级、恢复与验证

- 升级前做一致性备份。云端保存 `cloud-data`（SQLite）、`provision`（自动登记）及 `secrets/`。现场保存 `recorder-data` 和站点 Compose、环境文件及本地通道配置。Frigate 数据由其独立部署负责。
- 升级时核对 `VERSION`、云端和边缘镜像标记。更新服务时保留数据卷。升级后确认登录、站点在线、实时播放和已配置的录像查询正常。
- 开发验证使用 `go test ./...`。前端在 `web` 执行 `npm ci` 和 `npm run build`。与已有实例并行联调时使用独立端口和凭据。
