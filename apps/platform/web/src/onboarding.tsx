import {Button} from '@/components/ui/button';
import {Input} from '@/components/ui/input';
import {useState} from 'react';
import {api} from './accounts';
import {Copy,Eye,EyeOff} from 'lucide-react';

export type Channel = {id:string;name:string;ptz?:boolean;ptzSource?:'catalog'|'manual'|'unknown'};
type Diagnostics = {
  registration:'waiting'|'online'|'offline';failure?:string;
  catalog:'waiting'|'querying'|'request_failed'|'timeout'|'save_failed'|'ready';
  lastSignal?:string;lastAttempt?:string;catalogReceived?:number;catalogTotal?:number;
};
export type Device = {id:string;name:string;kind:'camera'|'recorder';online:boolean;channels:Channel[];diagnostics:Diagnostics};
export type Platform = {sipAddress:string;sipId:string;sipRealm:string;sipPort:number};
type Setup = Platform & {deviceId:string;secret:string;protocol:string;transport:string};

const failures:Record<string,string>={
  challenge:'已收到注册请求，等待设备完成密码认证。',
  invalid_digest:'设备发来的 SIP 认证格式无效。',
  username_mismatch:'SIP 认证 ID 与设备编码不一致。',
  realm_mismatch:'SIP 域不匹配，请核对设备端的域。',
  stale_challenge:'认证已过期，请在设备端重新发起注册。',
  bad_password:'国标注册密码校验失败，请核对设备端填写的密码。',
  server_id_mismatch:'SIP 服务器 ID 不匹配，请核对设备端的服务器编码。',
  registration_expired:'注册已过期，等待设备重新注册。',
  heartbeat_timeout:'心跳超时，请检查设备网络和心跳配置。',
  unregistered:'设备已主动注销。',
};

function catalogStatus(d:Diagnostics, count:number){
  const progress=d.catalogTotal?('（'+(d.catalogReceived??0)+'/'+d.catalogTotal+' 条目录记录）'):'';
  switch(d.catalog){
    case 'querying':return '正在获取设备目录'+progress+'。';
    case 'request_failed':return '设备未确认目录查询，请检查设备的国标目录响应。';
    case 'timeout':return '等待设备返回完整目录超时'+progress+'。';
    case 'save_failed':return '目录已收到，保存失败。';
    case 'ready':return count?'已自动发现 '+count+' 路视频通道。':'设备目录已返回，未发现视频通道。';
    default:return '设备注册后会自动查询通道。';
  }
}

export function SetupPanel({device,platform,onEdit,onRefresh}:{device:Device;platform:Platform;onEdit:()=>void;onRefresh:()=>void}){
  const [section,setSection]=useState<'status'|'parameters'>(device.online?'status':'parameters');
  const [copying,setCopying]=useState(false);
  const [feedback,setFeedback]=useState('');
  const [manual,setManual]=useState('');
  const [ptzSaving,setPTZSaving]=useState('');
  const [ptzError,setPTZError]=useState('');
  const [secret,setSecret]=useState<string>();
  const [readingSecret,setReadingSecret]=useState(false);
  const d=device.diagnostics;
  const label=device.online?'已注册 · 在线':d.registration==='offline'?'离线':d.failure&&d.failure!=='challenge'?'注册失败':'等待注册';
  const fields=(p:Platform,id:string)=>[
    ['SIP 服务器地址',p.sipAddress],['SIP 服务器端口',String(p.sipPort)],
    ['SIP 服务器 ID',p.sipId],['SIP 域',p.sipRealm],
    ['设备编码（SIP 用户 ID）',id],['SIP 认证 ID',id],
  ];
  async function setPTZ(channel:Channel,enabled:boolean,automatic=false){
    setPTZSaving(channel.id);setPTZError('');
    try{
      await api('/api/devices/'+device.id+'/'+channel.id+'/ptz',{method:'PUT',body:JSON.stringify({enabled,automatic})});
      onRefresh();
    }catch(error){setPTZError((error as Error).message)}
    finally{setPTZSaving('')}
  }
  async function copy(){
    setCopying(true);setFeedback('');setManual('');
    const config=api<Setup>('/api/devices/'+device.id+'/setup',{method:'POST'}).then(s=>[
      ...fields(s,s.deviceId),['国标注册密码',s.secret],['信令传输',s.transport],
      ['协议版本',s.protocol],
    ].map(([key,value])=>key+'：'+value).join('\n'));
    try{
      if(navigator.clipboard&&typeof ClipboardItem!=='undefined'){
        await navigator.clipboard.write([new ClipboardItem({'text/plain':config.then(value=>new Blob([value],{type:'text/plain'}))})]);
      }else{
        await navigator.clipboard.writeText(await config);
      }
      setFeedback('已复制，可粘贴到设备配置旁逐项填写。');
    }catch{
      try{
        setManual(await config);
        setFeedback('浏览器未允许写入剪贴板，请手动复制下方配置。');
      }catch(error){setFeedback((error as Error).message)}
    }finally{setCopying(false)}
  }
  async function copyField(value:string){
    setFeedback('');
    try{await navigator.clipboard.writeText(value);setFeedback('已复制。')}
    catch{setManual(value);setFeedback('浏览器未允许写入剪贴板，请手动复制下方内容。')}
  }
  async function revealSecret(){
    if(secret!==undefined){setSecret(undefined);return}
    setReadingSecret(true);setFeedback('');
    try{const setup=await api<Setup>('/api/devices/'+device.id+'/setup',{method:'POST'});setSecret(setup.secret)}
    catch(error){setFeedback((error as Error).message)}
    finally{setReadingSecret(false)}
  }
  return <section className="details setup" aria-labelledby="setup-title">
    <div className="setup-top"><div><h2 id="setup-title">国标接入</h2><p>{device.name} · {device.kind==='recorder'?'硬盘录像机':'网络摄像头'}</p></div>
      </div>
    <div className="registry-filters setup-tabs" role="group" aria-label="接入详情视图">
      <Button variant="outline" aria-pressed={section==='status'} onClick={()=>setSection('status')}>注册与通道</Button>
      <Button variant="outline" aria-pressed={section==='parameters'} onClick={()=>setSection('parameters')}>接入参数</Button>
    </div>
    {section==='parameters'&&<div>
    <p>在摄像头或录像机的管理页打开「平台接入」，选择 GB28181。复制下方配置，按设备页面逐项填写，再启用注册。</p>
    <dl className="setup-fields">
      {[...fields(platform,device.id),['信令传输','UDP'],['协议版本','GB/T 28181-2016']].map(([key,value])=><div key={key}><dt>{key}</dt><dd><span>{value}</span><Button variant="ghost" size="icon" aria-label={'复制'+key} onClick={()=>void copyField(value)}><Copy size={13}/></Button></dd></div>)}
      <div><dt>国标注册密码</dt><dd><span>{secret??'••••••••'}</span><Button variant="ghost" size="icon" disabled={readingSecret} aria-label={secret===undefined?'显示国标注册密码':'隐藏国标注册密码'} onClick={()=>void revealSecret()}>{secret===undefined?<Eye size={13}/>:<EyeOff size={13}/>}</Button>{secret!==undefined&&<Button variant="ghost" size="icon" aria-label="复制国标注册密码" onClick={()=>void copyField(secret)}><Copy size={13}/></Button>}</dd></div>
    </dl>
    <Button variant="outline" className="primary" type="button" disabled={copying} onClick={()=>void copy()}>{copying?'正在读取…':'复制国标配置'}</Button>
    {feedback&&<p role="status">{feedback}</p>}
    {manual&&<div className="manual-copy"><label>国标配置（含注册密码）<textarea value={manual} readOnly rows={6} onFocus={event=>event.currentTarget.select()}/></label><Button variant="outline" type="button" onClick={()=>setManual('')}>隐藏配置</Button></div>}
    </div>}
    {section==='status'&&<div>
    <div className="setup-progress">
      <h3>注册状态 <span className="setup-state" data-online={device.online}>{label}</span></h3>
      {d.failure?<p>{failures[d.failure]??'设备注册未完成。'}</p>:!device.online?<p>尚未收到此设备的注册请求。请核对设备编码、服务器地址、端口和网络。</p>:null}
      {d.lastAttempt&&<p className="setup-time">最近注册尝试 {new Date(d.lastAttempt).toLocaleString('zh-CN')}</p>}
      {d.lastSignal&&<p className="setup-time">最近有效信令 {new Date(d.lastSignal).toLocaleString('zh-CN')}</p>}
    </div>
    <div className="setup-progress">
      <h3>自动发现的通道</h3>
      <p>{catalogStatus(d,device.channels.length)}</p>
      {device.channels.length>0&&<>
        {d.catalog!=='ready'&&<p className="setup-time">以下为上次发现的通道。</p>}
        <ul className="setup-channels">{device.channels.map(channel=><li key={channel.id}>
          <div><strong>{channel.name}</strong><div>{channel.id}</div></div>
          <div><label className="ptz-toggle"><Input type="checkbox" checked={!!channel.ptz} disabled={!!ptzSaving} onChange={event=>void setPTZ(channel,event.target.checked)}/> 启用云台操作</label>
            <small>{ptzSaving===channel.id?'正在保存…':channel.ptzSource==='catalog'?'根据设备上报自动识别':channel.ptzSource==='manual'?'已手动设置':'设备未报告云台能力，可手动设置'}</small>
            {channel.ptzSource==='manual'&&<Button variant="outline" type="button" disabled={!!ptzSaving} onClick={()=>void setPTZ(channel,false,true)}>恢复自动识别</Button>}</div>
        </li>)}</ul>
        {ptzError&&<p role="alert" className="vh-error">{ptzError}</p>}
      </>}
    </div>
    </div>}
  </section>;
}
