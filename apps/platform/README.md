# ViewHub 平台应用

运行在服务器，负责客户空间、账号权限、设备接入、观看会话、现场录像查询、云台、对讲，以及向浏览器提供 WebRTC / WebCodecs / HLS。USB 采集和设备侧编码由独立设备端负责。

## 目录

- `cmd/gateway`：启动装配、HTTP API、鉴权和观看会话。
- `gb`：平台侧国标信令及 SQLite 数据。
- `media`：接收 PS/RTP、解析媒体及浏览器播放输出。
- `web`：React + shadcn/ui 平台管理界面，含播放器单元测试。
- `deploy`、`Dockerfile`、`compose.yaml`：平台部署资产。

## 本地开发

以下命令均从仓库根目录执行：

```powershell
npm --prefix apps/platform/web ci
npm --prefix apps/platform/web run build
go run ./apps/platform/cmd/gateway
```

默认静态页面目录为 `apps/platform/web/dist`，可用 `WEB_ROOT` 覆盖；`DB_PATH`、端口、平台编号和认证通过现有环境变量配置。开发时使用独立数据库和端口，不连接生产设备。数据库和口令留在私有目录。

## 构建和测试

```powershell
go test ./apps/platform/...
npm --prefix apps/platform/web test
./apps/platform/build.ps1
```

构建脚本产出到根目录 `dist/platform`，可用 `-OutputDirectory` 指定独立目录；不会启动或部署服务。解压运行产物时，将 `WEB_ROOT` 配为产物中的 `web` 目录。

容器构建使用仓库根作为 context：

```powershell
docker build -f apps/platform/Dockerfile --target cloud -t viewhub-cloud:0.6.0 .
```

镜像仅包含平台二进制和平台页面，不打包 USB 设备端或 FFmpeg。
