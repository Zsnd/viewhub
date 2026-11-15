import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Camera,
  Check,
  LoaderCircle,
  Play,
  RefreshCw,
  Save,
  Square,
  Video,
  Wifi,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {NativeSelect,NativeSelectOption} from "@/components/ui/native-select";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Field,
  FieldLabel,
  FieldDescription,
  FieldGroup,
} from "@/components/ui/field";
import { Separator } from "@/components/ui/separator";
import "./theme.css";
import "./style.css";

type Config = {
  ffmpeg: string;
  camera: string;
  width: number;
  height: number;
  fps: number;
  maxBitrate: number;
  quality: "standard"|"high"|"ultra";
  bitrateMode: "auto"|"manual";
  codec: "h264"|"h265";
  server: string;
  port: number;
  realm: string;
  serverId: string;
  deviceId: string;
  channelId: string;
  password?: string;
  localIp: string;
  sipPort: number;
};
type Status = {
  running: boolean;
  registered: boolean;
  streaming: boolean;
  frames: number;
  bytes: number;
  error: string;
  events: { at: string; message: string }[];
};
async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const r = await fetch("/api/" + path, {
    method,
    headers:
      method === "GET"
        ? {}
        : { "Content-Type": "application/json", "X-ViewHub-Capture": "1" },
    body: method === "GET" ? undefined : JSON.stringify(body ?? {}),
  });
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}
function App() {
  const [config, setConfig] = useState<Config | null>(null),
    [status, setStatus] = useState<Status | null>(null),
    [cameras, setCameras] = useState<string[]>([]),
    [modes,setModes]=useState<{width:number;height:number;fps:number}[]|null>(null),
    [busy, setBusy] = useState(""),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [recommendation,setRecommendation]=useState<number|null>(null),
    [recommendationError,setRecommendationError]=useState(false),
    [dirty, setDirty] = useState(false);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    api<Config>("config")
      .then((c) => {
        if (active) setConfig(c);
      })
      .catch((e) => {
        if (active) setError(e.message);
      });
    async function poll() {
      try {
        const s = await api<Status>("status");
        if (active) setStatus(s);
      } catch (e) {
        if (active) setError((e as Error).message);
      } finally {
        if (active) timer = setTimeout(poll, 1500);
      }
    }
    void poll();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, []);
  useEffect(()=>{
    if(!config)return;
    let active=true;
    setRecommendation(null);setRecommendationError(false);
    const query=new URLSearchParams({width:String(config.width),height:String(config.height),fps:String(config.fps),codec:config.codec,quality:config.quality});
    api<{maxBitrate:number}>('video-recommendation?'+query).then(value=>{
      if(!active)return;
      setRecommendation(value.maxBitrate);
      setConfig(current=>current?.bitrateMode==='auto'?{...current,maxBitrate:value.maxBitrate}:current);
    }).catch(()=>{if(active)setRecommendationError(true)});
    return()=>{active=false};
  },[config?.width,config?.height,config?.fps,config?.codec,config?.quality]);
  async function action(name: string, fn: () => Promise<unknown>) {
    setBusy(name);
    setError("");
    setNotice("");
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  }
  function update<K extends keyof Config>(key: K, value: Config[K]) {
    if(key==="camera")setModes(null);
    setConfig((c) => (c ? { ...c, [key]: value, ...(key==='maxBitrate'?{bitrateMode:'manual' as const}:{}) } : c));
    setDirty(true);
    setNotice("");
  }
  if (!config)
    return (
      <main className="loading" role="status">
        {error || "正在读取设备配置…"}
      </main>
    );
  const c = config,
    locked = Boolean(status?.running || busy),
    state = status?.streaming
      ? "正在发送视频"
      : status?.registered
        ? "已注册 · 等待点播"
        : status?.running
          ? "正在连接平台"
          : "设备已停止";
  function field(
    key: keyof Config,
    label: string,
    type = "text",
    hint?: string,
    min?: number,
    max?: number,
  ) {
    const value = c[key];
    const invalid =
      type === "number" &&
      (!Number.isInteger(value) ||
        (min !== undefined && Number(value) < min) ||
        (max !== undefined && Number(value) > max));
    return (
      <Field className="field" data-invalid={invalid || undefined}>
        <FieldLabel htmlFor={key}>{label}</FieldLabel>
        <Input
          id={key}
          type={type}
          aria-invalid={invalid || undefined}
          value={c[key] ?? ""}
          disabled={locked}
          min={min}
          max={max}
          step={type === "number" ? 1 : undefined}
          autoComplete={key === "password" ? "new-password" : "off"}
          placeholder={
            key === "password"
              ? "首次填写；留空保留已保存密码"
              : key === "localIp"
                ? "自动选择网卡"
                : undefined
          }
          onChange={(e) =>
            update(
              key,
              type === "number"
                ? e.target.value === ""
                  ? 0
                  : Number(e.target.value)
                : e.target.value,
            )
          }
        />
        {hint && <FieldDescription>{hint}</FieldDescription>}
      </Field>
    );
  }
  return (
    <div className="app">
      <header>
        <a className="brand" href="/" aria-label="ViewHub Capture 首页">
          <Camera aria-hidden="true" />
          <span>
            ViewHub <strong>Capture</strong>
          </span>
        </a>
        <Badge variant={status?.registered ? "default" : "secondary"}>
          {status?.registered ? <Wifi /> : <Square />}
          {state}
        </Badge>
      </header>
      <main>
        <div className="heading">
          <div>
            <h1>把 USB 摄像头接入 ViewHub</h1>
            <p>在本机采集，通过 GB28181 向平台提供实时画面。</p>
          </div>
          <span className="local-only">仅本机配置 · 单路视频</span>
        </div>
        {error && (
          <div className="feedback error" role="alert">
            {error}
          </div>
        )}
        {notice && (
          <div className="feedback" role="status">
            <Check aria-hidden="true" />
            {notice}
          </div>
        )}
        <div className="workspace">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void action("save", async () => {
                const saved = await api<Config>("config", "PUT", c);
                setConfig(saved);
                setDirty(false);
                setNotice("配置已保存。点击「启动设备」向平台注册。");
              });
            }}
          >
            <section className="panel">
              <div className="section-title">
                <Video aria-hidden="true" />
                <div>
                  <h2>视频源</h2>
                  <p>摄像头仅在平台点播后开始采集，停止点播即释放。</p>
                </div>
              </div>
              <div className="source-row">
                <div className="field grow">
                  <label htmlFor="camera">摄像头名称 / Linux 设备路径</label>
                  <Input
                    id="camera"
                    list="camera-list"
                    value={c.camera}
                    disabled={locked}
                    onChange={(e) => update("camera", e.target.value)}
                  />
                  <datalist id="camera-list">
                    {cameras.map((n) => (
                      <option key={n} value={n} />
                    ))}
                  </datalist>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  disabled={locked}
                  onClick={() =>
                    void action("cameras", async () => {
                      const names = await api<string[]>("cameras");
                      setCameras(names);
                      const caps=await api<{modes:{width:number;height:number;fps:number}[]}>("capabilities?camera="+encodeURIComponent(c.camera));setModes(caps.modes);
                      setNotice(
                        names.length
                          ? `发现 ${names.length} 个视频源，可在摄像头输入框中选择。`
                          : "未发现摄像头，请检查 USB 连接。",
                      );
                    })
                  }
                >
                  {busy === "cameras" ? (
                    <LoaderCircle className="spin" />
                  ) : (
                    <RefreshCw />
                  )}
                  检测设备
                </Button>
              </div>
              <FieldGroup className="fields two">
                <Field className="field"><FieldLabel htmlFor="resolution">分辨率</FieldLabel><NativeSelect id="resolution" disabled={locked} value={`${c.width}x${c.height}`} onChange={e=>{const [width,height]=e.target.value.split('x').map(Number);const mode=modes?.find(m=>m.width===width&&m.height===height);setConfig({...c,width,height,fps:Math.min(c.fps,mode?.fps??c.fps)});setDirty(true)}}>
                {[...new Set([`${c.width}x${c.height}`,...(modes?.length?modes.map(m=>`${m.width}x${m.height}`):['1920x1080','1280x720','800x600','640x480','640x360'])])].map(v=><NativeSelectOption key={v} value={v}>{v.replace('x',' × ')}</NativeSelectOption>)}
                </NativeSelect></Field>
                <Field className="field"><FieldLabel htmlFor="fps">输出帧率</FieldLabel><NativeSelect id="fps" value={c.fps} disabled={locked} onChange={e=>update('fps',Number(e.target.value))}>{[...new Set([c.fps,5,10,15,20,25,30,50,60])].sort((a,b)=>a-b).filter(f=>f<=(modes?.find(m=>m.width===c.width&&m.height===c.height)?.fps??60)).map(f=><NativeSelectOption key={f} value={f}>{f} fps</NativeSelectOption>)}</NativeSelect></Field>
                <Field className="field"><FieldLabel htmlFor="codec">视频编码</FieldLabel><NativeSelect id="codec" value={c.codec??'h264'} disabled={locked} onChange={e=>update('codec',e.target.value as Config['codec'])}><NativeSelectOption value="h264">H.264 · 兼容优先</NativeSelectOption><NativeSelectOption value="h265">H.265 / HEVC</NativeSelectOption></NativeSelect></Field>
                <Field className="field"><FieldLabel htmlFor="quality">画质</FieldLabel><NativeSelect id="quality" value={c.quality} disabled={locked} aria-describedby="quality-help" onChange={e=>update('quality',e.target.value as Config['quality'])}><NativeSelectOption value="standard">标准 · 节省带宽</NativeSelectOption><NativeSelectOption value="high">高清 · 画质与带宽均衡</NativeSelectOption><NativeSelectOption value="ultra">更高 · 保留更多细节</NativeSelectOption></NativeSelect><FieldDescription id="quality-help">实际码率随画面变化；达到上限时可能降低画质。</FieldDescription></Field>
                <Field className="field" data-invalid={!Number.isInteger(c.maxBitrate)||c.maxBitrate<128||c.maxBitrate>20000||undefined}>
                  <FieldLabel htmlFor="maxBitrate">最大码率（kbps）</FieldLabel>
                  <Input id="maxBitrate" type="number" min={128} max={20000} step={1} value={c.maxBitrate||''} disabled={locked} aria-describedby="bitrate-help" aria-invalid={!Number.isInteger(c.maxBitrate)||c.maxBitrate<128||c.maxBitrate>20000} onChange={e=>update('maxBitrate',Number(e.target.value))}/>
                  <FieldDescription id="bitrate-help">{recommendation!==null?`建议 ${recommendation.toLocaleString()} kbps`:(recommendationError?'建议暂不可用，保存时会重新计算':'正在计算建议…')} · {c.bitrateMode==='auto'?'随视频配置自动更新':'已手动设置，切换配置仍保留'}。建议是起点，不是实际带宽预测。</FieldDescription>
                  <Button type="button" variant="outline" size="sm" disabled={locked||recommendation===null||c.bitrateMode==='auto'} onClick={()=>{setConfig({...c,maxBitrate:recommendation!,bitrateMode:'auto'});setDirty(true);setNotice('')}}>使用建议值</Button>
                </Field>
              </FieldGroup>
              <p className="help">{modes?.length?`已读取 ${modes.length} 种 MJPEG 采集规格；输出帧率可低于采集帧率。`:'点击「检测设备」确认摄像头实际支持的分辨率与最大帧率。'}码率上限按编码缓冲窗口控制，网络瞬时峰值和传输开销可能更高。AV1 留作后续。</p>
              <details>
                <summary>采集程序</summary>
                {field(
                  "ffmpeg",
                  "FFmpeg 可执行文件",
                  "text",
                  "可填写系统命令名或本机绝对路径。",
                )}
              </details>
            </section>
            <section className="panel">
              <div className="section-title">
                <Wifi aria-hidden="true" />
                <div>
                  <h2>国标平台</h2>
                  <p>使用 ViewHub 分配的设备编号和注册密码。</p>
                </div>
              </div>
              <div className="fields address">
                {field("server", "平台 IP / 主机名")}
                {field("port", "SIP 端口", "number", undefined, 1, 65535)}
              </div>
              <div className="fields two">
                {field("serverId", "平台编号")}
                {field("realm", "SIP 域")}
                {field("deviceId", "设备编号")}
                {field("channelId", "通道编号")}
              </div>
              {field(
                "password",
                "注册密码",
                "password",
                "密码保存在本机，不会通过读取配置接口返回。",
              )}
              <details>
                <summary>本机网络</summary>
                <div className="fields two">
                  {field("localIp", "本机 IPv4 地址")}
                  {field(
                    "sipPort",
                    "本地 SIP 端口",
                    "number",
                    undefined,
                    1024,
                    65535,
                  )}
                </div>
                <p className="help">
                  媒体采用 UDP，平台媒体地址需与 SIP 平台 IP 一致。
                </p>
              </details>
              <Separator />
              <div className="save-row">
                <span>
                  {status?.running
                    ? "设备运行期间配置已锁定"
                    : dirty
                      ? "有未保存的配置"
                      : "配置保存在本机"}
                </span>
                <Button type="submit" disabled={locked}>
                  {busy === "save" ? (
                    <LoaderCircle className="spin" />
                  ) : (
                    <Save />
                  )}
                  保存配置
                </Button>
              </div>
            </section>
          </form>
          <aside>
            <section className="panel status-panel">
              <h2>设备运行</h2>
              <div className="device-symbol">
                <Camera aria-hidden="true" />
              </div>
              <h3>{state}</h3>
              <p>
                {status?.streaming
                  ? "真实视频正在发送到平台，请在 ViewHub 查看画面。"
                  : status?.registered
                    ? "在 ViewHub 中选择 USB 通道，点击播放。"
                    : dirty
                      ? "设置已修改，点击「保存并启动设备」即可应用并连接平台。"
                      : "点击「启动设备」向平台注册。启动表示允许平台点播本机摄像头。"}
              </p>
              <Button
                className="run-button"
                disabled={Boolean(busy)}
                variant={status?.running ? "outline" : "default"}
                onClick={() =>
                  void action("run", async () => {
                    if(!status?.running&&dirty){
                      const saved=await api<Config>("config","PUT",c);
                      setConfig(saved);setDirty(false);
                    }
                    const s = await api<Status>(
                      status?.running ? "stop" : "start",
                      "POST",
                    );
                    setStatus(s);
                  })
                }
              >
                {busy === "run" ? (
                  <LoaderCircle className="spin" />
                ) : status?.running ? (
                  <Square />
                ) : (
                  <Play />
                )}
                {status?.running ? "停止设备" : dirty ? "保存并启动设备" : "启动设备"}
              </Button>
              <dl>
                <div>
                  <dt>视频格式</dt>
                  <dd>{c.codec==='h265'?'H.265':'H.264'} / PS / UDP</dd>
                </div>
                <div>
                  <dt>已发送帧</dt>
                  <dd>{(status?.frames ?? 0).toLocaleString()}</dd>
                </div>
                <div>
                  <dt>已发送数据</dt>
                  <dd>{((status?.bytes ?? 0) / 1048576).toFixed(2)} MiB</dd>
                </div>
              </dl>
              {status?.error && (
                <div className="feedback error" role="alert">
                  {status.error}
                </div>
              )}
            </section>
            <section className="panel logs">
              <h2>运行日志</h2>
              <p className="help">最近 80 条事件，不保存视频画面。</p>
              <ol aria-label="运行日志">
                {status?.events?.length ? (
                  status.events
                    .slice()
                    .reverse()
                    .map((e, i) => (
                      <li key={e.at + i}>
                        <time>{e.at}</time>
                        <span>{e.message}</span>
                      </li>
                    ))
                ) : (
                  <li className="empty">启动设备后显示注册与采集事件。</li>
                )}
              </ol>
            </section>
          </aside>
        </div>
      </main>
      <footer>ViewHub Capture · 摄像头连接在运行此服务的电脑上</footer>
    </div>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
