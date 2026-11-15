# ViewHub 架构与开发

## 应用边界

仓库维护两个独立进程。平台部署到服务器，USB 设备端部署到摄像头所在电脑；设备端通过 GB/T 28181 注册和传输媒体，浏览器连接平台观看。两端不通过 Go 函数调用、数据库或本机配置文件交换业务数据。

```text
USB 摄像头 → Capture（Go + FFmpeg） ── SIP / PS-RTP ──→ Platform（Go） → 浏览器
                 ↑ 本机配置页                              ↑ 平台管理台
```

| 目录 | 职责 |
| --- | --- |
| `apps/platform/cmd/gateway` | 平台进程入口、HTTP API、鉴权、观看和对讲会话 |
| `apps/platform/gb` | 平台国标信令、设备库存与 SQLite 数据 |
| `apps/platform/media` | 平台媒体接收、解析、浏览器播放输出 |
| `apps/platform/web` | 平台 React 页面及页面逻辑测试 |
| `apps/platform/deploy` | 平台反向代理配置 |
| `apps/capture` | 设备配置、生命周期、编码和国标媒体发送 |
| `apps/capture/cmd/capture` | 设备端进程入口 |
| `apps/capture/web` | 仅回环访问的设备本机配置页 |
| `tests/integration` | 通过真实本地 SIP socket 验证两个应用协作 |
| `tests/architecture` | 防止应用内生产代码和单元测试跨应用引用 |

两端各自维护前端 package.json、lockfile 和组件。保留根 Go 模块 `viewhub`，应用独立构建；共用依赖版本不代表应用互相依赖。`gb` 和 `media` 位于平台应用内，导出接口供平台装配及仓库级联合测试使用，不作为客户端库。暂不抽公共国标 SDK，也不合并两个界面的状态。

## 构建入口

所有命令从仓库根目录执行。

| 目标 | 命令 | 输出 |
| --- | --- | --- |
| 平台 Go | `go build -o dist/platform/viewhub.exe ./apps/platform/cmd/gateway` | 平台进程；Linux 去掉 .exe |
| 设备端 Go | `go build -o dist/capture/viewhub-capture.exe ./apps/capture/cmd/capture` | 设备端进程；Linux 去掉 .exe |
| 平台页面 | `npm --prefix apps/platform/web run build` | `apps/platform/web/dist` |
| 设备页面 | `npm --prefix apps/capture/web run build` | `apps/capture/web/dist` |
| 平台完整产物 | `./apps/platform/build.ps1` | 默认 `dist/platform`，含 web |
| Windows 设备完整产物 | `./apps/capture/build.ps1 -OutputDirectory ./dist/capture` | exe、web 和 start.cmd |

各前端首次构建前，在对应 web 目录运行 `npm ci`。Windows 打包脚本自动安装前端依赖；设备端默认安装位置仍为用户本机目录，验证构建时应指定独立 OutputDirectory，避免覆盖正在运行的程序。

Linux / ARM64 交叉构建可设置 `GOOS=linux`、`GOARCH=arm64` 后构建设备端入口。构建成功不代表已完成摄像头或硬件编码实机验收。

## 测试分层

| 范围 | 命令 | 边界 |
| --- | --- | --- |
| 平台 | `go test ./apps/platform/...` | 平台权限、国标和媒体逻辑 |
| 设备端 | `go test ./apps/capture/...` | 配置、编码选择、SDP 和媒体封装；不引入平台或 SQLite |
| 两端联合 | `go test ./tests/integration/...` | 临时 SQLite、回环 SIP socket、注册/目录/空录像/注销和重复启动 |
| 架构边界 | `go test ./tests/architecture/...` | 阻止应用目录内跨应用导入 |
| 平台页面 | `npm --prefix apps/platform/web test` | 播放意图、断线重试、停止及迟到响应 |
| 全仓验证 | `./scripts/check.ps1` | Go 测试和 vet、页面测试、两端页面构建、Compose 校验 |

默认自动测试不访问生产环境、不打开 USB 摄像头，不部署应用。设备端真实 FFmpeg 合成画面检测需要显式设置 `VIEWHUB_TEST_FFMPEG`，不属于默认检查。

## 配置与发布

根 `compose.yaml` 是稳定的部署入口，包含 `apps/platform/compose.yaml`；平台 Dockerfile 使用仓库根构建上下文。继续从仓库根执行 Compose，保留原项目名、数据卷、端口和私有 `.env`、`secrets`。不要仅因目录移动就更换 Compose 项目名，否则可能连接到新的空数据卷。

平台容器内仍是 `/viewhub`、`/web`、`/data/viewhub.db`；设备端 HTTP 地址、私有配置位置、配置字段、安装产物名保持不变。本轮目录整理不改变 API、国标编号、媒体协议或编码策略，不自动发布任何一端。
