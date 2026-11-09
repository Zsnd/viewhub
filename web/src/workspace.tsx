import {useCallback,useEffect,useRef,useState,type RefObject} from 'react';
import {Accounts,api,headers,type Account} from './accounts';
import {type Device,type Platform} from './onboarding';
import {DeviceSettings,type EdgeSite} from './device-settings';
import {Icon} from './icons';
import {EdgeReplay} from './edge-replay';
import {HistoryControls} from './history-controls';
import {clock,type Day} from './timeline';
import {Player as EdgePlayer} from './edge-player';
import {GBPlayer,type Session} from './gb-player';

type Inventory=Platform&{devices:Device[]};
type Channel={key:string;name:string;device:string;base:string;online:boolean;ptz:boolean;history:boolean;zone:string;edge:boolean;source:string;recordingOnly?:boolean};
type Wall={size:number;slots:string[]};
type Connection='WebRTC 直连'|'WebRTC 网关'|'服务器中继'|'WebCodecs'|'HLS'|'MP4';
type Window={slot:string;revision:number;at:number;end:number;mode:Day['mode']};
const noop=()=>{};
const post=<T,>(path:string,data:object={})=>api<T>(path,{method:'POST',body:JSON.stringify(data)});
const timezone='Asia/Shanghai';
function today(zone:string){return new Intl.DateTimeFormat('sv-SE',{timeZone:zone}).format(new Date())}
function epoch(value:string){return Date.parse(/[zZ]|[+-]\d\d:\d\d$/.test(value)?value:value+'+08:00')/1000}

function Feed({channel,record,video,onPosition,onPaused,onStatus,onConnection,onEnd}:{channel:Channel;record?:Window;video?:RefObject<HTMLVideoElement|null>;onPosition?:(time:number)=>void;onPaused?:(paused:boolean)=>void;onStatus?:(status:string)=>void;onConnection?:(method:Connection|undefined)=>void;onEnd?:(status:string)=>void}){
 const [session,setSession]=useState<Session>();
 const [status,setStatus]=useState('正在连接…');
 const report=useCallback((message:string)=>{setStatus(message);onStatus?.(message)},[onStatus]);
 const ended=useCallback((message:string)=>{setStatus(message);onEnd?.(message)},[onEnd]);
 useEffect(()=>{
  if(channel.edge)return;
  let disposed=false,sessionID='';
  const release=(id:string)=>{void fetch('/api/session/'+id,{method:'DELETE',headers,keepalive:true}).catch(noop)};
  const body=record?{start:new Date(record.at*1000).toISOString(),end:new Date(record.end*1000).toISOString()}:{};
  void post<Session>(channel.base+'/play',body).then(value=>{
   if(disposed){release(value.id);return}
   sessionID=value.id;
   setSession(value);
  }).catch(e=>{if(!disposed)ended(e.message)});
  return ()=>{disposed=true;if(sessionID)release(sessionID)};
 },[channel.base,channel.edge,record,ended]);
 if(channel.edge){
  if(record?.mode==='mp4'&&video)return <EdgeReplay base={channel.base} video={video} {...record} onPosition={onPosition} onEnd={ended} onStatus={report}
   onPlaying={method=>{onPaused?.(false);onConnection?.(method);report('正在播放 · 历史录像')}}/>;
  const hls=record?channel.base+'/vod/'+record.at+'/'+record.end+'/index.m3u8':'';
  return <><EdgePlayer videoRef={video} rtc={record?undefined:channel.base+'/rtc'} live={record?undefined:channel.base+'/relay'} hls={hls} onStatus={report} onConnection={onConnection} onEnd={ended} onPosition={onPosition} onPaused={onPaused}/>{status.includes('失败')&&<span className="feed-status">{status}</span>}</>;
 }
 return session?<><GBPlayer session={session} onStatus={report} onConnection={onConnection} onEnd={ended} onFallback={noop} videoRef={video} onPosition={onPosition} onPaused={onPaused}/>{!status.startsWith('正在播放')&&<span className="feed-status">{status}</span>}</>:<span className="feed-status">{status}</span>;
}

function Monitor({channel,onPTZChanged}:{channel:Channel;onPTZChanged:()=>void}){
 const [date,setDate]=useState(()=>today(channel.zone));
 const [day,setDay]=useState<Day>();
 const [busy,setBusy]=useState(false);
 const [mode,setMode]=useState<'live'|'history'>(channel.recordingOnly?'history':'live');
 const [live,setLive]=useState(false);
 const [isFullscreen,setIsFullscreen]=useState(false);
 const [window,setWindow]=useState<Window>();
 const [position,setPosition]=useState<number>();
 const [paused,setPaused]=useState(true);
 const [time,setTime]=useState('00:00:00');
 const [status,setStatus]=useState('等待播放');
 const [connection,setConnection]=useState<Connection>();
 const [error,setError]=useState('');
 const [preset,setPreset]=useState('');
 const video=useRef<HTMLVideoElement>(null);
 const seekRevision=useRef(0);
 const [slot]=useState(()=>crypto.randomUUID());
 const screen=useRef<HTMLDivElement>(null);
 useEffect(()=>{
  const update=()=>setIsFullscreen(document.fullscreenElement===screen.current);
  document.addEventListener('fullscreenchange',update);
  return ()=>document.removeEventListener('fullscreenchange',update);
 },[]);
 const end=useCallback((message:string)=>{setWindow(undefined);setLive(false);setConnection(undefined);setStatus(message)},[]);
 useEffect(()=>{
  if(mode!=='history'||!date||!channel.history)return;
  const abort=new AbortController();
  setBusy(true);setDay(undefined);setError('');
  async function load(){
   if(channel.edge)return api<Day>(channel.base+'/recordings?date='+date,{signal:abort.signal});
   const start=epoch(date+'T00:00:00'),end=start+86400;
   const query=new URLSearchParams({start:new Date(start*1000).toISOString(),end:new Date(end*1000).toISOString()});
   const rows=await api<{start:string;end:string}[]>(channel.base+'/records?'+query,{signal:abort.signal});
   return {start,end,mode:'hls' as const,source:'gb' as const,segments:rows.map(r=>({start:Math.max(start,epoch(r.start)),end:Math.min(end,epoch(r.end))})).filter(r=>r.start<r.end).sort((a,b)=>a.start-b.start)};
  }
  void load().then(d=>{if(!abort.signal.aborted)setDay(d)}).catch(e=>{if(!abort.signal.aborted)setError(e.message)}).finally(()=>{if(!abort.signal.aborted)setBusy(false)});
  return ()=>abort.abort();
 },[channel.base,channel.edge,channel.history,date,mode]);
 function seek(at:number){
  const row=day?.segments.find(s=>s.start<=at&&at<s.end);
  if(!row||!day){end('选择录像时刻');setError(clock(at,channel.zone)+' 没有录像');return}
  setError('');setPosition(at);setTime(clock(at,channel.zone));setLive(false);setPaused(false);
  setConnection(undefined);setStatus('正在连接…');
  setWindow({slot,revision:++seekRevision.current,at,end:channel.edge&&day.mode==='mp4'?row.end:Math.min(row.end,at+3600),mode:day.mode});
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
  if(window?.mode==='mp4'&&channel.edge){
   end('已暂停');setPaused(true);return;
  }
  const element=video.current;
  if(window&&element){if(element.paused)void element.play().catch(noop);else element.pause()}
  else if(day?.segments.length)seek(position??day.segments[0].start);
 }
 function move(action:string){
  setError('');
  void post(channel.base+'/ptz',{action,preset:channel.edge?preset:Number(preset)}).catch(e=>setError(e.message));
 }
 async function toggleFullscreen(){
  try {
   if(document.fullscreenElement===screen.current)await document.exitFullscreen();
   else await screen.current?.requestFullscreen();
  }catch{setError('此浏览器暂不支持全屏')}
 }
 const next=day?.mode==='mp4'&&status==='录像播放完毕'&&position!==undefined?day?.segments.find(segment=>segment.start>position):undefined;
 const fullscreen=<button type="button" disabled={!isFullscreen&&!connection} onClick={()=>void toggleFullscreen()}><Icon name="fullscreen"/>{isFullscreen?'退出全屏':'全屏'}</button>;
 return <div className="watch-monitor">
 <section className="watch-stage" data-mode={mode}>
 <header className="stage-heading"><div><p>{channel.device===channel.source?channel.device:channel.device+' · '+channel.source}</p><h2>{channel.name}</h2></div></header>
 {channel.history&&!channel.recordingOnly&&<div className="watch-modes" role="group" aria-label="观看模式">
  <button aria-pressed={mode==='live'} onClick={()=>{if(mode==='live')return;end('等待播放');setMode('live');setError('')}}>实时画面</button>
  <button aria-pressed={mode==='history'} onClick={()=>{if(mode==='history')return;end('选择录像时刻');setMode('history');setError('')}}>历史录像</button>
 </div>}
 <div className="watch-media">
 <div ref={screen} className="vh-screen">
  <span className="watch-connection" role="status">连接 · {connection??(live||window?'连接中':channel.online?'未连接':'通道离线')}</span>{live||window?<Feed key={window?.revision??'live'} channel={channel} record={window} video={video} onStatus={setStatus} onConnection={setConnection} onEnd={end}
  onPosition={window?seconds=>{if(Number.isFinite(seconds))setPosition(Math.min(window.end-1,window.at+Math.max(0,Math.floor(seconds))))}:undefined} onPaused={window?setPaused:undefined}/>:mode==='history'?<p>{channel.online?'从时间轴选择录像时刻':'通道离线'}</p>:null}
 {mode==='live'&&<div className="player-controls" role="group" aria-label="实时播放控制">
   {!live&&<div className="player-start">
     <button className="player-play" type="button" disabled={!channel.online} aria-label="播放" onClick={()=>{setStatus('正在连接…');setLive(true)}}>
       <svg viewBox="0 0 24 24" width="27" height="27" fill="currentColor" aria-hidden="true"><path d="M8 4.6a1 1 0 0 0-1.5.86v13.08a1 1 0 0 0 1.5.86l11-6.54a1 1 0 0 0 0-1.72z"/></svg>
     </button>
     <span role="status">{channel.online?(status==='等待播放'?'播放实时画面':status):'通道离线'}</span>
   </div>}
   {(live||isFullscreen)&&<div className="player-toolbar">
     {live&&<button type="button" onClick={()=>end('已停止')}>停止</button>}
     {fullscreen}
   </div>}
 </div>}
 {mode==='history'&&isFullscreen&&<div className="player-toolbar">{fullscreen}</div>}
 </div>
 </div>

 {mode==='history'&&<HistoryControls day={day} date={date} zone={channel.zone} busy={busy} position={position} time={time} error={error} active={!!window} paused={paused}
  onDate={next=>{end('选择录像时刻');setDate(next);setPosition(undefined)}} onTime={setTime} onJump={jump} onSeek={seek} onToggle={toggleHistory} onStop={()=>end('已停止')} status={connection?undefined:status} onNext={next?()=>seek(next.start):undefined} fullscreen={fullscreen}/>}
 </section>
 <aside className="channel-dock" aria-label="云台与通道"><div><h3>云台与通道</h3>
 {!channel.edge&&<label className="ptz-toggle"><input type="checkbox" checked={channel.ptz} onChange={e=>void api(channel.base+'/ptz',{method:'PUT',body:JSON.stringify({enabled:e.target.checked})}).then(onPTZChanged).catch(e=>setError(e.message))}/> 此通道支持国标云台</label>}
 {channel.ptz?<><div className="ptz-directions" role="group" aria-label="云台方向">
 {([['上','up'],['左','left'],['停止','stop'],['右','right'],['下','down']] as const).map(([label,id])=>
 <button key={id} data-direction={id} aria-label={id==='stop'?'停止云台':label} disabled={!channel.online} onClick={()=>move(id)}>{label}</button>)}
 </div><div className="ptz-zoom" role="group" aria-label="云台变焦">
 <button disabled={!channel.online} onClick={()=>move('in')}>放大</button><button disabled={!channel.online} onClick={()=>move('out')}>缩小</button></div>
 <form className="vh-actions" onSubmit={e=>{e.preventDefault();move('preset')}}><input aria-label="预置位" placeholder="预置位" type={channel.edge?'text':'number'} min={1} max={255} value={preset} onChange={e=>setPreset(e.target.value)} required/><button disabled={!channel.online}>前往</button></form></>:<p>此通道未启用云台。</p>}
 </div><dl className="watch-meta"><dt>接入方式</dt><dd>{channel.source}</dd><dt>录像来源</dt><dd>{channel.history?(channel.source==='Frigate'?'Frigate 本地录像':'现场录像机'):'此通道未提供'}</dd></dl>
 {mode!=='history'&&error&&<p role="alert" className="vh-error">{error}</p>}</aside>
 </div>;
}

function ChannelList({channels,active,onSelect}:{channels:Channel[];active?:string;onSelect:(key:string)=>void}){
 const [search,setSearch]=useState('');
 const [expanded,setExpanded]=useState(false);
 const query=search.trim().toLocaleLowerCase();
 const matches=channels.filter(c=>[c.name,c.device,c.source].join(' ').toLocaleLowerCase().includes(query));
 return <aside className="channel-list" data-expanded={expanded} aria-label="站点与通道">
  <div className="channel-list-heading"><h2>站点与通道</h2><button aria-expanded={expanded} aria-controls="watch-channel-list" onClick={()=>setExpanded(!expanded)}>{expanded?'收起通道':'选择通道'}</button></div>
  <div className="channel-picker" id="watch-channel-list">
  <input type="search" aria-label="搜索站点或通道" placeholder="搜索站点或通道" value={search} onChange={e=>setSearch(e.target.value)}/>
  {!matches.length&&<p className="muted">没有匹配的通道。</p>}
  <ul>{matches.map(c=><li key={c.key}>
   <button className="watch-channel" aria-pressed={active===c.key} onClick={()=>{onSelect(c.key);setExpanded(false)}}>
    <span><strong>{c.name}</strong><small>{c.device===c.source?c.source:c.device+' · '+c.source}</small></span>
    <small className="device-state" data-online={c.online}>{c.online?'在线':'离线'}</small>
   </button>
  </li>)}</ul>
 </div></aside>;
}


export function Workspace({account,onLogout}:{account:Account;onLogout:()=>void}){
 const [page,setPage]=useState<'wall'|'watch'|'devices'|'accounts'>('wall');
 const pages=[{id:'wall',label:'监控墙'},{id:'watch',label:'视频工作台'},{id:'devices',label:'设备接入'},{id:'accounts',label:'客户空间'}] as const;
 const pageTitle=pages.find(item=>item.id===page)?.label;
 const [inventory,setInventory]=useState<Inventory>();
 const [edges,setEdges]=useState<EdgeSite[]>([]);
 const [wall,setWall]=useState<Wall>();
 const [selected,setSelected]=useState('');
 const [slot,setSlot]=useState<number>();
 const [slotValue,setSlotValue]=useState('');
 const [saving,setSaving]=useState(false);
 const wallLayout=useRef<HTMLDialogElement>(null);
 const [error,setError]=useState('');
 const grid=useRef<HTMLDivElement>(null);
 const dialog=useRef<HTMLDialogElement>(null);
 const refresh=useCallback(()=>{
  void api<Inventory>('/api/devices').then(setInventory).catch(e=>setError(e.message));
  void api<EdgeSite[]>('/api/edge').then(setEdges).catch(e=>setError(e.message));
 },[]);
 useEffect(()=>{
  refresh();
  void api<Wall>('/api/wall').then(setWall).catch(e=>setError(e.message));
  const timer=setInterval(refresh,5000);return()=>clearInterval(timer);
 },[refresh]);
 useEffect(()=>{
  if(slot!==undefined&&!dialog.current?.open)dialog.current?.showModal();
 },[slot]);
 const channels:Channel[]=[
  ...((inventory?.devices??[]).flatMap(d=>d.channels.map(c=>({key:'gb/'+d.id+'/'+c.id,name:c.name,device:d.name,base:'/api/devices/'+d.id+'/'+c.id,online:d.online,ptz:!!c.ptz,history:d.kind==='recorder',zone:timezone,edge:false,source:'国标直连'})))),
  ...edges.flatMap(s=>s.channels.map(c=>({key:'edge/'+s.id+'/'+c.id,name:c.name,device:c.recorderName??(s.provider==='edge'?'局域网设备':s.name),base:'/api/edge/'+s.id+'/'+c.id,online:s.online,ptz:c.ptz,history:c.history,zone:c.timezone,edge:true,recordingOnly:c.recordingOnly,source:s.provider==='frigate'?'Frigate':'轻边缘网关'}))),
 ];
 const liveChannels=channels.filter(c=>!c.recordingOnly);
 const channel=channels.find(c=>c.key===selected)??channels[0];
 async function save(next:Wall){
  setSaving(true);setError('');
  try{await api('/api/wall',{method:'PUT',body:JSON.stringify(next)});setWall(next);dialog.current?.close();wallLayout.current?.close()}
  catch(e){setError((e as Error).message)}
  finally{setSaving(false)}
 }
 return <div className="vh-shell">
  <nav className="vh-nav" aria-label="主导航">
    <div className="vh-brand" aria-label="ViewHub"><Icon name="brand"/></div>
    {pages.filter(item=>item.id!=='accounts'||account.admin).map(({id,label})=><button key={id} title={label} aria-current={id===page?'page':undefined} onClick={()=>{setPage(id);setError('')}}><Icon name={id}/><span>{label}</span></button>)}
  </nav>
  <div className="vh-body"><header className="vh-top">
   <div className="top-context"><strong>ViewHub</strong><span>{account.space}</span></div>
   <button className="signout" aria-label="退出登录" title="退出登录" onClick={()=>void post('/api/auth/logout').then(onLogout).catch(e=>setError(e.message))}><Icon name="logout"/><span>退出</span></button>
  </header>
  <main className={page==='wall'?'vh-sheet wall-sheet':'vh-sheet'}>
   {page!=='devices'&&<div className="page-heading"><h1>{pageTitle}</h1><div className="vh-actions">
   {page==='wall'&&wall&&<><button onClick={()=>wallLayout.current?.showModal()}>{wall.size} 分屏 <span aria-hidden="true">⌄</span></button>
   <button onClick={()=>void grid.current?.requestFullscreen().catch(()=>setError('当前浏览器无法进入全屏'))}><Icon name="fullscreen"/>全屏</button></>}
   </div></div>}
   {error&&<p role="alert" className="vh-error">{error}</p>}
   {page==='wall'&&(!wall?<p>正在读取监控墙…</p>:<div ref={grid} className="vh-grid" style={{'--columns':Math.sqrt(wall.size)} as React.CSSProperties}>
    {Array.from({length:wall.size},(_,index)=>{
     const item=liveChannels.find(c=>c.key===wall.slots[index]);
     return <div className="vh-tile" key={index}>{item?.online?<Feed key={item.key} channel={item}/>:<span className="wall-placeholder">{item?'通道离线':wall.slots[index]?'等待通道连接':<><Icon name="plus"/>选择通道</>}</span>}
      <button className="vh-slot" aria-label={'第 '+(index+1)+' 格 · '+(item?.name??'选择通道')} onClick={()=>{if(document.fullscreenElement)return;setSlotValue(wall.slots[index]??'');setSlot(index)}}><span>{item?.name??'选择通道'}</span></button></div>;
    })}
   </div>)}
   {page==='watch'&&<>
    {channel?<div className="watch-columns"><ChannelList channels={channels} active={channel.key} onSelect={setSelected}/><Monitor key={channel.key} channel={channel} onPTZChanged={refresh}/></div>:<p className="vh-empty">设备接入后，通道会出现在这里。</p>}</>}
   {page==='devices'&&<DeviceSettings inventory={inventory} edges={edges} onRefresh={refresh}/>}
   {page==='accounts'&&account.admin&&<Accounts/>}
  </main></div>
  <dialog ref={wallLayout} aria-labelledby="wall-layout-title">
   <div className="device-dialog-heading"><h2 id="wall-layout-title">选择分屏</h2><button onClick={()=>wallLayout.current?.close()}>关闭</button></div>
   {wall&&<div className="wall-layout-options">{[4,9,16].map(size=><button key={size} aria-pressed={wall.size===size} disabled={saving} onClick={()=>void save({...wall,size})}>
    <span className="layout-preview" style={{gridTemplateColumns:'repeat('+Math.sqrt(size)+', 1fr)'}} aria-hidden="true">{Array.from({length:size},(_,index)=><i key={index}/>)}</span>
    <span>{size} 分屏</span>
   </button>)}</div>}
   {error&&<p role="alert" className="vh-error">{error}</p>}
  </dialog>


  <dialog ref={dialog} aria-labelledby="wall-dialog-title" onClose={()=>setSlot(undefined)}><form onSubmit={e=>{e.preventDefault();if(!wall||slot===undefined)return;const slots=Array.from({length:Math.max(wall.slots.length,slot+1)},(_,i)=>i===slot?slotValue:wall.slots[i]??'');void save({...wall,slots})}}>
   <h2 id="wall-dialog-title">选择通道 · 第 {(slot??0)+1} 格</h2>
   <select aria-label="关联通道" value={slotValue} onChange={e=>setSlotValue(e.target.value)}><option value="">空画面</option>{slotValue&&!liveChannels.some(c=>c.key===slotValue)&&<option value={slotValue}>当前通道暂不可用</option>}{liveChannels.map(c=><option key={c.key} value={c.key}>{c.device} / {c.name}{c.online?'':'（离线）'}</option>)}</select>
   {error&&<p role="alert" className="vh-error">{error}</p>}
   <div className="vh-actions"><button type="button" onClick={()=>dialog.current?.close()}>取消</button><button className="primary" disabled={saving}>确定</button></div>
  </form></dialog>
 </div>;
}
