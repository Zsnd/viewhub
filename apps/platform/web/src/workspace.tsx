import {useCallback,useEffect,useImperativeHandle,useRef,useState,type RefObject} from 'react';
import {Accounts,PasswordSettings,api,headers,type Account} from './accounts';
import {type Device,type Platform} from './onboarding';
import {DeviceSettings} from './device-settings';
import {Icon} from './icons';
import {Button} from '@/components/ui/button';
import {Input} from '@/components/ui/input';
import {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter} from '@/components/ui/dialog';
import {Monitor as MonitorIcon,Video,History,Camera,Users,Search,Grid2X2,Maximize,LogOut,ChevronRight,Radio,PanelLeft,Plus,ArrowUp,ArrowDown,ArrowLeft,ArrowRight,ZoomIn,ZoomOut,Play,Pause,Minimize,RotateCw} from 'lucide-react';
import {HistoryControls} from './history-controls';
import {clock,type Day} from './timeline';
import {GBPlayer,type Session} from './gb-player';
import {PlaybackSessionController,PlaybackFailure} from './playback-session';
import {TalkControls} from './talk-controls';
import {PlayerSurface,StopGlyph,useMediaFullscreen} from './player-surface';
import {PlayerVolume} from './player-volume';
import {useImageAdjustments} from './image-adjustments';
import {usePtzHold} from './ptz-hold';
import {SlidingSegments} from './sliding-segments';

type Inventory=Platform&{devices:Device[]};
type Channel={key:string;name:string;device:string;base:string;online:boolean;state:'online'|'offline'|'waiting'|'failed';ptz:boolean;zone:string};
type Wall={size:number;slots:string[]};
type Connection='WebRTC 网关'|'WebCodecs'|'HLS';
type Window={revision:number;at:number;end:number};
const noop=()=>{};
const post=<T,>(path:string,data:object={})=>api<T>(path,{method:'POST',body:JSON.stringify(data)});
const timezone='Asia/Shanghai';
const connectionError='连接服务器失败，请检查网络后重试';
function today(zone:string){return new Intl.DateTimeFormat('sv-SE',{timeZone:zone}).format(new Date())}
function epoch(value:string){return Date.parse(/[zZ]|[+-]\d\d:\d\d$/.test(value)?value:value+'+08:00')/1000}

function Feed({channel,record,video,muted=true,volume=1,onPosition,onPaused,onStatus,onConnection,onEnd,onAudioStatus}:{channel:Channel;record?:Window;video?:RefObject<HTMLVideoElement|null>;muted?:boolean;volume?:number;onPosition?:(time:number)=>void;onPaused?:(paused:boolean)=>void;onStatus?:(status:string)=>void;onConnection?:(method:Connection|undefined)=>void;onEnd?:(status:string)=>void;onAudioStatus?:(status:string)=>void}){
 const [session,setSession]=useState<Session>();
 const [status,setStatus]=useState('正在连接…');
 const driver=useRef<PlaybackSessionController|null>(null);
 const report=useCallback((message:string)=>{setStatus(message);onStatus?.(message);if(message.startsWith('正在播放'))driver.current?.played()},[onStatus]);
 const ended=useCallback((message:string)=>driver.current?.failed(message),[]);
 useEffect(()=>{
  const body=record?{start:new Date(record.at*1000).toISOString(),end:new Date(record.end*1000).toISOString()}:{};
  const controller=new PlaybackSessionController({
   live:!record,
   create:async signal=>{
    const response=await fetch(channel.base+'/play',{method:'POST',headers,body:JSON.stringify(body),signal:AbortSignal.any([signal,AbortSignal.timeout(12000)])});
    if(!response.ok){
     if(response.status===401)window.dispatchEvent(new Event('login-expired'));
     const data=await response.json().catch(()=>({}));
     throw new PlaybackFailure(data.error??'无法连接设备',response.status);
    }
    return response.json() as Promise<Session>;
   },
   release:value=>fetch('/api/session/'+value.id,{method:'DELETE',headers,keepalive:true,signal:AbortSignal.timeout(3500)}),
   update:setSession,status:report,
   end:message=>{setStatus(message);onEnd?.(message)},
  });
  driver.current=controller;controller.start();
  return ()=>{controller.stop();if(driver.current===controller)driver.current=null};
 },[channel.base,record,report,onEnd]);
 return session?<><GBPlayer key={session.id} realtime={!record} onAudioStatus={onAudioStatus} muted={muted} volume={volume} session={session} onStatus={report} onConnection={onConnection} onEnd={ended} videoRef={video} onPosition={onPosition} onPaused={onPaused}/>{!status.startsWith('正在播放')&&<span className="feed-status">{status}</span>}</>:<span className="feed-status" role="status">{status}</span>;
}

type FullscreenHandle={toggle:()=>Promise<void>};
function Monitor({channel,fullscreenRef,playRequest}:{channel:Channel;fullscreenRef:RefObject<FullscreenHandle|null>;playRequest:number}){
 const [date,setDate]=useState(()=>today(channel.zone));
 const [day,setDay]=useState<Day>();
 const [busy,setBusy]=useState(false);
 const [mode,setMode]=useState<'live'|'history'>('live');
 const [live,setLive]=useState(false);
 const [listening,setListening]=useState(false);
 const [volume,setVolume]=useState(1);
 const image=useImageAdjustments();
 const [audioStatus,setAudioStatus]=useState('');
 const [window,setWindow]=useState<Window>();
 const [position,setPosition]=useState<number>();
 const [paused,setPaused]=useState(true);
 const [time,setTime]=useState('00:00:00');
 const [status,setStatus]=useState('等待播放');
 const [connection,setConnection]=useState<Connection>();
 const [error,setError]=useState('');
 const ptz=usePtzHold(channel.base,channel.online&&channel.ptz&&mode==='live',setError);
 useEffect(()=>{if(playRequest>0&&channel.online){setWindow(undefined);setMode('live');setLive(true);setStatus('正在连接…')}},[playRequest]);
 const [preset,setPreset]=useState('');
 const video=useRef<HTMLVideoElement>(null);
 const seekRevision=useRef(0);
 const screen=useRef<HTMLDivElement>(null);
 const {expanded,active:isFullscreen,toggle:toggleFullscreen}=useMediaFullscreen(screen);
 useImperativeHandle(fullscreenRef,()=>({toggle:toggleFullscreen}),[expanded]);
 const end=useCallback((message:string)=>{setListening(false);setAudioStatus('');setWindow(undefined);setLive(false);setConnection(undefined);setStatus(message)},[]);
 useEffect(()=>{
  if(mode!=='history'||!date)return;
  const abort=new AbortController();
  setBusy(true);setDay(undefined);setError('');
  async function load(){
   const start=epoch(date+'T00:00:00'),end=start+86400;
   const query=new URLSearchParams({start:new Date(start*1000).toISOString(),end:new Date(end*1000).toISOString()});
   const rows=await api<{start:string;end:string}[]>(channel.base+'/records?'+query,{signal:abort.signal});
   return {start,end,segments:rows.map(r=>({start:Math.max(start,epoch(r.start)),end:Math.min(end,epoch(r.end))})).filter(r=>r.start<r.end).sort((a,b)=>a.start-b.start)};
  }
  void load().then(d=>{if(!abort.signal.aborted)setDay(d)}).catch(e=>{if(!abort.signal.aborted)setError(e.message)}).finally(()=>{if(!abort.signal.aborted)setBusy(false)});
  return ()=>abort.abort();
 },[channel.base,date,mode]);
 function seek(at:number){
  const row=day?.segments.find(s=>s.start<=at&&at<s.end);
  if(!row||!day){end('选择录像时刻');setError(clock(at,channel.zone)+' 没有录像');return}
  setError('');setPosition(at);setTime(clock(at,channel.zone));setLive(false);setPaused(false);
  setConnection(undefined);setStatus('正在连接…');
  setWindow({revision:++seekRevision.current,at,end:Math.min(row.end,at+3600)});
 }
 function jump(){
  if(!day)return;
  const value=time.length===5?time+':00':time;
  const [h,m,s]=value.split(':').map(Number);
  for(let at=day.start+s;at<day.end;at+=60){
   if(clock(at,channel.zone)===value){seek(at);return}
  }
  if([h,m,s].every(Number.isFinite))setError('所选时间无效');
 }
 function toggleHistory(){
  const element=video.current;
  if(window&&element){if(element.paused)void element.play().catch(noop);else element.pause()}
  else if(day?.segments.length)seek(position??day.segments[0].start);
 }
 function move(action:string){
  setError('');
  void post(channel.base+'/ptz',{action,preset:Number(preset)}).catch(e=>setError(e.message));
 }
 function switchMode(next:'live'|'history'){
  if(next===mode)return;
  end(next==='live'?'等待播放':'选择录像时刻');setError('');setMode(next);setListening(false);
  if(next==='live'&&channel.online){setStatus('正在连接…');setLive(true)}
 }
 const retry=!live&&!['等待播放','已停止','正在连接…'].includes(status);
 const fullscreen=<button className="media-control" type="button" aria-label={isFullscreen?'退出全屏':'全屏视频'} title={isFullscreen?'退出全屏':'全屏视频'} onClick={()=>void toggleFullscreen()}>{isFullscreen?<Minimize size={18}/>:<Maximize size={18}/>}</button>;
 return <div className="watch-monitor" data-ptz={channel.ptz} data-controls="true">
 <section className="watch-stage" data-mode={mode}>
 <header className="stage-heading"><div><h2>{channel.name}</h2><p>{channel.device}</p></div><span className="status-badge" data-online={channel.online}>{channel.online?'设备在线':'设备离线'}</span></header>
 <div className="watch-mode-bar"><SlidingSegments className="watch-mode-switch" value={mode} label="观看模式">
  <Button variant={mode==='live'?'secondary':'ghost'} size="sm" aria-pressed={mode==='live'} onClick={()=>switchMode('live')}><Video size={15}/>实时画面</Button>
  <Button variant={mode==='history'?'secondary':'ghost'} size="sm" aria-pressed={mode==='history'} onClick={()=>switchMode('history')}><History size={15}/>录像回放</Button>
 </SlidingSegments></div>


 <div className="watch-media" style={image.style}>
 <PlayerSurface screen={screen} expanded={expanded} playing={!!connection&&(mode==='live'?live:!!window&&!paused)} onToggle={()=>{if(mode==='history')toggleHistory();else if(live)end('已停止');else if(channel.online){setStatus('正在连接…');setLive(true)}}} onFullscreen={()=>void toggleFullscreen()}>
  <span className="watch-connection" role="status">连接 · {connection??(live||window?'连接中':channel.online?'未连接':'通道离线')}</span>{live||window?<Feed key={window?.revision??'live'} channel={channel} record={window} video={video} muted={!listening} volume={volume} onAudioStatus={setAudioStatus} onStatus={setStatus} onConnection={setConnection} onEnd={end}
  onPosition={window?seconds=>{if(Number.isFinite(seconds))setPosition(Math.min(window.end-1,window.at+Math.max(0,Math.floor(seconds))))}:undefined} onPaused={window?setPaused:undefined}/>:mode==='history'?<p>{channel.online?'从时间轴选择录像时刻':'通道离线'}</p>:null}
 <div className="player-controls" role="group" aria-label={mode==='live'?'实时播放控制':'录像播放控制'}>
   {mode==='live'&&!live&&<div className="player-start">
     {mode==='live'?<button className={retry?"player-retry":"player-play"} type="button" disabled={!channel.online} aria-label={retry?"重新播放":"播放实时画面"} title={retry?"重新播放":"播放实时画面"} onClick={()=>{setStatus('正在连接…');setLive(true)}}>{retry?<><RotateCw size={18}/>重新播放</>:<Play size={27} fill="currentColor"/>}</button>:null}
     <span role="status">{mode==='live'?(channel.online?(status==='等待播放'?'播放实时画面':status):'通道离线'):''}</span>
   </div>}
   <div className="player-toolbar">
     {mode==='live'?<button className="media-control" type="button" disabled={!channel.online&&!live} aria-label={live?'停止播放':'播放实时画面'} title={live?'停止播放':'播放实时画面'} onClick={()=>live?end('已停止'):(setStatus('正在连接…'),setLive(true))}>{live?<StopGlyph/>:<Play size={18} fill="currentColor"/>}</button>:<>
       <button className="media-control" type="button" disabled={!day?.segments.length} aria-label={window&&!paused?'暂停录像':'播放录像'} title={window&&!paused?'暂停录像':'播放录像'} onClick={toggleHistory}>{window&&!paused?<Pause size={18}/>:<Play size={18} fill="currentColor"/>}</button>
       <button className="media-control" type="button" disabled={!window} aria-label="停止回放" title="停止回放" onClick={()=>end('已停止')}><StopGlyph/></button>
     </>}
     <PlayerVolume active={listening} disabled={!connection} volume={volume} onToggle={()=>setListening(previous=>volume===0?true:!previous)} onVolume={next=>{setVolume(next);if(next>0)setListening(true)}}/>
     {mode==='history'&&<span className="player-time">{position===undefined?'--:--:--':clock(position,channel.zone)}</span>}
     <span className="player-toolbar-spacer"/>
     {fullscreen}
   </div>
 </div>
 </PlayerSurface>
 </div>

 {mode==='history'&&<HistoryControls day={day} date={date} zone={channel.zone} busy={busy} position={position} time={time} error={error}
  onDate={next=>{end('选择录像时刻');setDate(next);setPosition(undefined)}} onTime={setTime} onJump={jump} onSeek={seek} status={connection?undefined:status}/>}

 {audioStatus&&<p className="audio-status" role="status">{volume===0&&audioStatus==='正在播放现场声音'?'已静音':audioStatus}</p>}
 <dl className="watch-meta"><dt>接入方式</dt><dd>GB28181</dd><dt>录像来源</dt><dd>现场设备</dd></dl>
 {mode!=='history'&&error&&<p role="alert" className="vh-error">{error}</p>}
 </section>
 <aside className="channel-dock" data-ptz={channel.ptz} aria-label="通道操作">
 <h2 className="dock-title">通道操作</h2>
 {channel.ptz&&<section className="ptz-controls" aria-label="云台操作"><h3>云台操作</h3>
 {mode==='history'&&<p className="dock-hint">切换至实时画面后可控制云台。</p>}
 <div className="ptz-directions" role="group" aria-label="云台方向">
 {([{label:'向上',action:'up',glyph:ArrowUp},{label:'向左',action:'left',glyph:ArrowLeft},{label:'停止云台',action:'stop',glyph:StopGlyph},{label:'向右',action:'right',glyph:ArrowRight},{label:'向下',action:'down',glyph:ArrowDown}] as const).map(({label,action,glyph:DirectionIcon})=>
 <Button key={action} variant="outline" size="icon" data-direction={action} aria-label={label} aria-pressed={action==='stop'?undefined:ptz.pressed===action} title={action==='stop'?label:label+'（按住转动，松开停止）'} disabled={!channel.online||mode==='history'}
 onPointerDown={event=>{if(action==='stop'||event.button!==0)return;event.preventDefault();event.currentTarget.setPointerCapture(event.pointerId);setError('');ptz.start(action)}} onPointerUp={()=>ptz.stop()} onPointerCancel={()=>ptz.stop()} onLostPointerCapture={()=>ptz.stop()} onBlur={()=>ptz.stop()}
 onKeyDown={event=>{if(action==='stop'||![' ','Enter'].includes(event.key))return;event.preventDefault();if(!event.repeat){setError('');ptz.start(action)}}} onKeyUp={event=>{if(action!=='stop'&&[' ','Enter'].includes(event.key)){event.preventDefault();ptz.stop()}}}
 onClick={event=>{if(action==='stop'){ptz.stop();move('stop')}else if(event.detail===0)move(action)}}><DirectionIcon size={17}/></Button>)}
 </div><div className="ptz-zoom" role="group" aria-label="云台变焦">
 <Button variant="outline" size="icon" aria-label="放大" title="放大" disabled={!channel.online||mode==='history'} onClick={()=>move('in')}><ZoomIn size={17}/></Button><Button variant="outline" size="icon" aria-label="缩小" title="缩小" disabled={!channel.online||mode==='history'} onClick={()=>move('out')}><ZoomOut size={17}/></Button></div>
 <form className="ptz-preset" onSubmit={e=>{e.preventDefault();move('preset')}}><label>预置位<Input aria-label="预置位" placeholder="1–255" type="number" min={1} max={255} value={preset} onChange={e=>setPreset(e.target.value)} disabled={!channel.online||mode==='history'} required/></label><Button variant="outline" disabled={!channel.online||mode==='history'}>前往</Button></form></section>}
 <TalkControls key={mode} base={channel.base} online={channel.online&&mode==='live'} disabledReason={mode==='history'?'切换至实时画面后可对讲。':!channel.online?'设备离线，恢复连接后可对讲。':undefined} onStart={()=>{setStatus('正在连接…');setLive(true);setListening(false)}}/>
 {image.controls}
 </aside>
 </div>;
}


const channelStateName={online:'在线',offline:'离线',waiting:'等待注册',failed:'注册失败'};

function ChannelList({channels,active,onSelect}:{channels:Channel[];active?:string;onSelect:(key:string)=>void}){
 const [search,setSearch]=useState('');
 const [online,setOnline]=useState(false);
 const [expanded,setExpanded]=useState(false);
 const matches=channels.filter(c=>(!online||c.online)&&[c.name,c.device].join(' ').toLowerCase().includes(search.trim().toLowerCase()));
 const groups=new Map<string,Channel[]>();for(const c of matches){const id=c.base.split('/')[3];groups.set(id,[...(groups.get(id)??[]),c])}
 return <aside className="channel-list" data-expanded={expanded} aria-label="视频通道">
  <header className="channel-list-heading"><h2>视频通道 <span>{channels.length}</span></h2><Button variant="ghost" size="sm" aria-expanded={expanded} onClick={()=>setExpanded(!expanded)}><PanelLeft size={16}/><span>选择通道</span></Button></header>
  <div className="channel-picker"><div className="search-field"><Search size={16}/><Input type="search" aria-label="搜索设备或通道" placeholder="搜索设备或通道" value={search} onChange={e=>setSearch(e.target.value)}/></div>
  <label className="online-filter"><input type="checkbox" checked={online} onChange={e=>setOnline(e.target.checked)}/>仅显示在线通道</label>
  <div className="channel-tree">{Array.from(groups,([device,rows])=><section key={device}><h3><Camera size={15}/>{rows[0].device}<span className="channel-state" data-state={rows[0].state}>{channelStateName[rows[0].state]}</span></h3><ul>{rows.map(c=><li key={c.key}><button className="watch-channel" aria-pressed={active===c.key} onClick={()=>{onSelect(c.key);setExpanded(false)}}><span className="signal-dot" data-online={c.online}/><span className="channel-label">{c.name}<small><span className="channel-state" data-state={c.state}>{channelStateName[c.state]}</span><span>{c.ptz?'云台通道':'视频通道'}</span></small></span><ChevronRight size={14}/></button></li>)}</ul></section>)}</div>
  {!matches.length&&<div className="list-empty">{channels.length?'没有匹配的通道':'接入设备后，通道会显示在这里'}</div>}</div>
 </aside>;
}

function WallTile({channel,index,onWatch,onConfigure,onFullscreen}:{channel?:Channel;index:number;onWatch:()=>void;onConfigure:()=>void;onFullscreen:()=>void}){
 const screen=useRef<HTMLDivElement>(null);
 const [connection,setConnection]=useState<Connection>();
 const [ended,setEnded]=useState('');
 const failed=useCallback((message:string)=>{setConnection(undefined);setEnded(message)},[]);
 useEffect(()=>{setEnded('');setConnection(undefined)},[channel?.key,channel?.online]);
 const name=channel?.name??`画面 ${index+1}`;
 return <PlayerSurface screen={screen} className="monitor-tile" label={`监控画面 ${index+1}`} expanded={false} playing={!!connection&&!ended&&!!channel?.online} onToggle={onWatch} onFullscreen={onFullscreen}>
 {channel?.online&&!ended?<Feed key={channel.key} channel={channel} onConnection={setConnection} onEnd={failed}/>:<div className="tile-empty"><Camera size={24}/><span>{ended|| (channel?'通道离线':'选择视频通道')}</span>{ended&&channel?.online&&<button className="player-retry" type="button" onClick={()=>setEnded('')}><RotateCw size={16}/>重新播放</button>}</div>}
 <div className="player-controls"><div className="player-toolbar tile-toolbar" role="group" aria-label={`画面 ${index+1} 操作`}>
 <button className="tile-title" type="button" disabled={!channel} title={name} onClick={onWatch}><span className="signal-dot" data-online={!!channel?.online}/><span>{name}</span></button>
 <button className="media-control" type="button" aria-label={'配置第 '+(index+1)+' 个画面'} title="配置通道" onClick={onConfigure}><Plus size={18}/></button>
 </div></div>
 </PlayerSurface>;
}

export function Workspace({account,onLogout,onPasswordChanged}:{account:Account;onLogout:()=>void;onPasswordChanged:()=>void}){
 const [page,setPage]=useState<'monitor'|'devices'|'accounts'>('monitor');
 const [inventory,setInventory]=useState<Inventory>();
 const [wall,setWall]=useState<Wall>();
 const [selected,setSelected]=useState('');
 const [view,setView]=useState(1);
 const [playRequest,setPlayRequest]=useState(0);
 const watch=(key:string)=>{setSelected(key);setView(1);setPlayRequest(value=>value+1)};
 const [slot,setSlot]=useState<number>();
 const [slotValue,setSlotValue]=useState('');
 const [saving,setSaving]=useState(false);
 const [error,setError]=useState('');
 const [mobileNav,setMobileNav]=useState(false);
 const grid=useRef<HTMLDivElement>(null);
 const wallFullscreen=useMediaFullscreen(grid);
 const singleFullscreen=useRef<FullscreenHandle|null>(null);
 const nav=useRef<HTMLElement>(null);
 const navToggle=useRef<HTMLButtonElement>(null);
 useEffect(()=>{if(!mobileNav)return;nav.current?.querySelector('button')?.focus();const escape=(event:KeyboardEvent)=>{if(event.key==='Escape'){setMobileNav(false);navToggle.current?.focus()}};window.addEventListener('keydown',escape);return()=>window.removeEventListener('keydown',escape)},[mobileNav]);
 const refresh=useCallback(()=>{void api<Inventory>('/api/devices').then(value=>{setInventory(value);setError(previous=>previous===connectionError?'':previous)}).catch(e=>setError(e instanceof TypeError?connectionError:e.message))},[]);
 useEffect(()=>{refresh();void api<Wall>('/api/wall').then(setWall).catch(e=>setError(e.message));const timer=setInterval(refresh,5000);return()=>clearInterval(timer)},[refresh]);
 const channels:Channel[]=(inventory?.devices??[]).flatMap(device=>device.channels.map((channel,index)=>({key:'gb/'+device.id+'/'+channel.id,name:device.channels.filter(c=>c.name===channel.name).length>1?channel.name+' · 通道 '+(index+1):channel.name,device:device.name,base:'/api/devices/'+device.id+'/'+channel.id,online:device.online,state:device.online?'online':device.diagnostics.registration==='offline'?'offline':device.diagnostics.failure&&device.diagnostics.failure!=='challenge'?'failed':'waiting',ptz:!!channel.ptz,zone:timezone})));
 const visible=channels;
 const channel=visible.find(c=>c.key===selected)??visible.find(c=>c.online)??visible[0];
 const navigation=[{id:'monitor',label:'视频监控',icon:Video},{id:'devices',label:'设备管理',icon:Camera},...(account.admin?[{id:'accounts',label:'空间管理',icon:Users}]:[])] as const;
 const title=navigation.find(n=>n.id===page)?.label;
 async function saveSlot(){
  if(slot===undefined||!wall)return;
  setSaving(true);setError('');
  const next={size:view,slots:Array.from({length:Math.max(wall.slots.length,slot+1)},(_,i)=>i===slot?slotValue:wall.slots[i]??'')};
  try{await api('/api/wall',{method:'PUT',body:JSON.stringify(next)});setWall(next);setSlot(undefined)}catch(e){setError((e as Error).message)}finally{setSaving(false)}
 }
 async function configureSlot(index:number){if(wallFullscreen.active)await wallFullscreen.toggle();setSlotValue(wall?.slots[index]??'');setSlot(index)}
 function navigate(id:string){setPage(id as typeof page);setError('');setMobileNav(false)}
 const work=page==='monitor';
 return <div className="app-shell" data-nav-open={mobileNav}>
 <aside ref={nav} id="app-navigation" className="app-sidebar"><a className="app-brand" href="#" onClick={e=>{e.preventDefault();navigate('monitor')}}><span><MonitorIcon size={21}/></span>ViewHub</a>
 <div className="space-context"><span className="space-avatar">{account.space.slice(0,1)}</span><div><strong>{account.space}</strong><small>当前客户空间</small></div></div>
 <nav aria-label="主导航">{navigation.map(({id,label,icon:NavIcon})=><button key={id} aria-current={id===page?'page':undefined} onClick={()=>navigate(id)}><NavIcon size={18}/>{label}</button>)}</nav>
 <div className="sidebar-bottom"><div className="account-identity"><span className="account-avatar">{account.username.slice(0,1).toUpperCase()}</span><div><strong>{account.username}</strong><small>{account.admin?'系统管理员':'空间成员'}</small></div></div><PasswordSettings onChanged={onPasswordChanged}/><button onClick={()=>void post('/api/auth/logout').then(onLogout).catch(e=>setError(e.message))}><LogOut size={16}/>退出登录</button></div></aside>
 {mobileNav&&<button className="nav-scrim" aria-label="关闭导航" onClick={()=>setMobileNav(false)}/>}
 <div className="app-body" inert={mobileNav}><header className="app-topbar"><Button ref={navToggle} className="mobile-nav-toggle" aria-expanded={mobileNav} aria-controls="app-navigation" variant="ghost" size="icon" aria-label="打开导航" onClick={()=>setMobileNav(true)}><PanelLeft size={20}/></Button><div className="breadcrumbs"><span>{account.space}</span><ChevronRight size={14}/><strong>{title}</strong></div><span className="topbar-health"><Radio size={14}/>{inventory?`${inventory.devices.filter(d=>d.online).length} 台设备在线`:'正在同步设备'}</span></header>
 <main className={work?'app-main monitoring-main':'app-main management-main'}>
 {page!=='devices'&&<header className="page-heading"><div><h1>{title}</h1><p>{page==='monitor'?'实时画面与录像回放，在同一通道中切换。':'管理客户空间与登录账号。'}</p></div>{page==='monitor'&&<div className="monitor-toolbar"><SlidingSegments className="view-selector" value={view} label="画面布局">{[1,4,9,16].map(n=><Button key={n} variant={view===n?'secondary':'ghost'} size="sm" aria-pressed={view===n} onClick={()=>setView(n)}>{n===1?<MonitorIcon size={15}/>:<Grid2X2 size={15}/>}<span>{n===1?'单画面':n+'分屏'}</span></Button>)}</SlidingSegments><Button variant="outline" size="sm" disabled={!channel} aria-label="全屏监控画面" onClick={()=>void (view===1?singleFullscreen.current?.toggle():wallFullscreen.toggle())}><Maximize size={16}/><span>全屏</span></Button></div>}</header>}
 {error&&<div role="alert" className="error-banner">{error}<Button variant="ghost" size="sm" onClick={()=>{setError('');refresh()}}>重试</Button></div>}
 {work&&<div className="monitor-layout"><ChannelList channels={visible} active={channel?.key} onSelect={watch}/><div className="monitor-workspace" data-layout={view===1?'single':'grid'}>
 {!inventory?<div className="workspace-empty" role="status">正在读取通道…</div>:!channel?<div className="workspace-empty"><Camera size={40}/><h2>{'尚未接入视频通道'}</h2><p>{'先添加设备，完成注册后即可查看现场画面。'}</p><Button onClick={()=>navigate('devices')}>前往设备管理<ChevronRight size={16}/></Button></div>:view===1?<Monitor key={channel.key} channel={channel} fullscreenRef={singleFullscreen} playRequest={playRequest}/>:<PlayerSurface screen={grid} className="monitor-wall" label="监控画面" expanded={wallFullscreen.expanded} playing={true} onToggle={noop} onFullscreen={()=>void wallFullscreen.toggle()}>
 <div className="monitor-grid" style={{'--columns':Math.sqrt(view)} as React.CSSProperties}>{Array.from({length:view},(_,index)=>{const item=channels.find(c=>c.key===wall?.slots[index]);return <WallTile key={index} channel={item} index={index} onWatch={()=>{if(item)watch(item.key)}} onConfigure={()=>void configureSlot(index)} onFullscreen={()=>void wallFullscreen.toggle()}/>})}</div>
 <button className="wall-fullscreen media-control" type="button" aria-label={wallFullscreen.active?'退出监控全屏':'全屏监控画面'} title={wallFullscreen.active?'退出监控全屏':'全屏监控画面'} onClick={()=>void wallFullscreen.toggle()}>{wallFullscreen.active?<Minimize size={18}/>:<Maximize size={18}/>}</button>
 </PlayerSurface>}

 </div></div>}
 {page==='devices'&&<DeviceSettings inventory={inventory} onWatch={key=>{watch(key);navigate('monitor')}} onRefresh={()=>{refresh();void api<Wall>('/api/wall').then(setWall).catch(e=>setError(e.message))}}/>}
 {page==='accounts'&&account.admin&&<Accounts/>}
 </main></div>
 <Dialog open={slot!==undefined} onOpenChange={open=>{if(!open&&!saving)setSlot(undefined)}}><DialogContent><DialogHeader><DialogTitle>配置画面 {(slot??0)+1}</DialogTitle><DialogDescription>选择该画面显示的视频通道，布局会保存到当前账号。</DialogDescription></DialogHeader><label>视频通道<select value={slotValue} onChange={e=>setSlotValue(e.target.value)}><option value="">留空</option>{channels.map(c=><option value={c.key} key={c.key}>{c.device} / {c.name}{c.online?'':'（离线）'}</option>)}</select></label><p role="alert" className="vh-error">{error}</p><DialogFooter><Button variant="outline" disabled={saving} onClick={()=>setSlot(undefined)}>取消</Button><Button disabled={saving||!wall} onClick={()=>void saveSlot()}>{saving?'正在保存…':'保存配置'}</Button></DialogFooter></DialogContent></Dialog>
 </div>;
}
