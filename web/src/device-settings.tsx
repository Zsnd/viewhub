import {useEffect,useRef,useState,type FormEvent} from 'react';
import {api} from './accounts';
import {RecorderSetup} from './recorder-setup';
import {SetupPanel,type Device,type Platform} from './onboarding';

export type EdgeSite = {
  id:string; name:string; provider:string; online:boolean;
  channels:{id:string;name:string;ptz:boolean;history:boolean;timezone:string;recordingOnly?:boolean;managedRecorder?:boolean;recorderId?:string;recorderName?:string;deviceKind?:'camera'|'recorder'}[];
};
type LocalDevice={id:string;site:EdgeSite;name:string;description:string;source:string;kind:"edge"|"frigate";channels:EdgeSite["channels"]};
type Inventory = Platform & {devices:Device[]};
type Detail = {mode:'add'} | {mode:'onvif';kind:'camera'|'recorder'} | {mode:'edit'|'setup';id:string} | {mode:'local';id:string};
const kindName = (kind:string) => kind === 'recorder' ? '硬盘录像机' : '摄像头';

function DeviceForm({device,onSaved,onCancel}:{device?:Device;onSaved:(id:string)=>void;onCancel:()=>void}){
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState('');
  async function save(event:FormEvent<HTMLFormElement>){
    event.preventDefault();
    setBusy(true);
    setError('');
    const data=Object.fromEntries(new FormData(event.currentTarget));
    try{
      await api(device?'/api/devices/'+device.id:'/api/devices',{
        method:device?'PUT':'POST',
        body:JSON.stringify({...data,name:String(data.name).trim(),id:device?.id??String(data.id).trim()}),
      });
      onSaved(device?.id??String(data.id).trim());
    }catch(error){setError((error as Error).message)}
    finally{setBusy(false)}
  }
  return <form className="device-form" onSubmit={save}>
    <label>设备名称<input autoFocus name="name" required maxLength={100} defaultValue={device?.name} placeholder="例如：仓库入口"/></label>
    <label>设备类型<select name="kind" defaultValue={device?.kind??'camera'}><option value="camera">摄像头</option><option value="recorder">硬盘录像机</option></select></label>
    {device?<div className="device-field"><span>设备编码</span><code>{device.id}</code><small>此编码用于识别设备和保留通道关联。</small></div>:
      <label>设备编码<input name="id" required pattern="[0-9]{20}" inputMode="numeric" maxLength={20} aria-describedby="device-id-hint"/><small id="device-id-hint">填写设备「平台接入」中的 20 位 SIP 用户 ID。</small></label>}
    <label>{device?'新国标注册密码（选填）':'国标注册密码'}<input name="secret" type="password" required={!device} minLength={8} maxLength={64} autoComplete="new-password" aria-describedby="device-secret-hint"/>
      <small id="device-secret-hint">{device?'留空继续使用原密码。填写新密码后，设备上的注册密码也要同步修改。':'设置 8–64 位密码，稍后在设备「平台接入」中填同一个密码。'}</small>
    </label>
    {error&&<p className="vh-error" role="alert">{error}</p>}
    <div className="vh-actions"><button type="submit" className="primary" disabled={busy}>{busy?'正在保存…':device?'保存修改':'保存并查看接入参数'}</button>
      <button type="button" disabled={busy} onClick={onCancel}>取消</button></div>
  </form>;
}

export function DeviceSettings({inventory,edges,onRefresh}:{inventory?:Inventory;edges:EdgeSite[];onRefresh:()=>void}){
  const [detail,setDetail]=useState<Detail>();
  const [notice,setNotice]=useState('');
  const [result,setResult]=useState('');
  const [removeError,setRemoveError]=useState('');
  const [removing,setRemoving]=useState(false);
  const [filter,setFilter]=useState<'all'|'gb'|'edge'|'frigate'>('all');
  const [query,setQuery]=useState('');
  const dialog=useRef<HTMLDialogElement>(null);
  const heading=useRef<HTMLHeadingElement>(null);
  const current=detail&&'id' in detail?inventory?.devices.find(d=>d.id===detail.id):undefined;
  useEffect(()=>{
    if(!detail)return;
    if(dialog.current&&!dialog.current.open)dialog.current.showModal();
    if(detail.mode!=='add')heading.current?.focus();
  },[detail]);
  function saved(id:string){
    setNotice(detail?.mode==='edit'?'已保存修改。':'已添加设备。请继续在设备上填写接入参数。');
    setDetail({mode:'setup',id});
    onRefresh();
  }
  function open(next?:Detail){setNotice('');setRemoveError('');setDetail(next)}
  async function removeRecorder(site:string,channel:string){
    if(!window.confirm('移除此 ONVIF 通道？'))return;
    setRemoving(true);
    setRemoveError('');
    try{
      await api('/api/edge/'+site+'/onvif',{method:'DELETE',body:JSON.stringify({channel})});
      setResult('已移除 ONVIF 通道。');
      dialog.current?.close();
      onRefresh();
    }catch(error){setRemoveError((error as Error).message)}
    finally{setRemoving(false)}
  }
  const devices=inventory?.devices.filter(d=>d.kind!=='frigate')??[];
  const local=edges.flatMap<LocalDevice>(site=>{
    if(site.provider==='frigate')return [{id:'frigate/'+site.id,site,name:site.name,description:'录像服务',source:'Frigate',kind:'frigate' as const,channels:site.channels}];
    const groups=new Map<string,LocalDevice>();
    for(const channel of site.channels){
      const id=site.id+'/'+(channel.recorderId??channel.id);
      const group=groups.get(id);
      if(group){group.channels.push(channel);continue}
      const camera=channel.deviceKind==='camera'||!channel.managedRecorder;
      groups.set(id,{id,site,name:channel.recorderName??channel.name,description:camera?'摄像头':'硬盘录像机',source:channel.managedRecorder?'ONVIF':'局域网',kind:'edge',channels:[channel]});
    }
    return [...groups.values()];
  });
  const localDetail=detail?.mode==='local'?local.find(item=>item.id===detail.id):undefined;
  const rows=[
    ...devices.map(d=>({id:d.id,name:d.name,kind:'gb' as const,source:'GB28181',description:kindName(d.kind),online:d.online,status:d.online?'在线':d.diagnostics.registration==='waiting'?'等待注册':'离线',count:d.channels.length})),
    ...local.map(item=>({...item,online:item.site.online,status:item.site.online?'已连接现场':'现场离线',count:item.channels.length})),
  ];
  const term=query.trim().toLocaleLowerCase();
  const visible=rows.filter(row=>(filter==='all'||row.kind===filter)&&[row.name,row.id,row.description,row.source].join(' ').toLocaleLowerCase().includes(term));
  return <section className="device-settings" aria-labelledby="device-heading">
    <header className="vh-heading device-heading">
      <div><h1 id="device-heading">设备接入</h1><p>管理摄像头、硬盘录像机和 Frigate 通道。</p></div>
      <div className="vh-actions">
        <button onClick={()=>open({mode:'add'})}>添加国标设备</button>
        <button onClick={()=>open({mode:'onvif',kind:'camera'})}>接入 ONVIF 摄像头</button>
        <button className="primary" onClick={()=>open({mode:'onvif',kind:'recorder'})}>接入 ONVIF 录像机</button>
      </div>
    </header>
    {result&&<p className="device-notice" role="status">{result}</p>}
    <div className="registry">
      <div className="registry-toolbar">
        <div className="registry-filters" role="group" aria-label="筛选接入方式">
          {([['all','全部'],['gb','国标'],['edge','局域网设备'],['frigate','Frigate']] as const).map(([key,label])=><button key={key} aria-pressed={filter===key} onClick={()=>setFilter(key)}>{label} <small>{rows.filter(row=>key==='all'||row.kind===key).length}</small></button>)}
        </div>
        <input type="search" aria-label="搜索设备" placeholder="搜索名称、编码或接入方式" value={query} onChange={e=>setQuery(e.target.value)}/>
      </div>
      {!visible.length&&<p className="vh-empty">{!inventory?'正在读取设备…':rows.length?'没有匹配的设备。':'此空间还没有可用设备。'}</p>}
      <ul className="registry-list">{visible.map(row=><li key={row.kind+'/'+row.id}>
        <button className="registry-row" onClick={()=>open({mode:row.kind==='gb'?'setup':'local',id:row.id})} aria-label={row.name+(row.kind==='gb'?' 接入详情':' 查看通道')}>
          <span className="registry-name"><strong>{row.name}</strong><small>{row.description}{row.kind==='gb'?' · '+row.id:''}</small></span>
          <span className="registry-source">{row.source}</span>
          <span className="device-state" data-online={row.online}>{row.status}</span>
          <span>{row.count} 路</span><span aria-hidden="true">›</span>
        </button>
      </li>)}</ul>
    </div>

    <dialog ref={dialog} className="device-dialog" aria-labelledby="device-dialog-heading" onClose={()=>open()}>
      {detail&&<>
        <div className="device-dialog-heading">
          <h2 id="device-dialog-heading" ref={heading} tabIndex={-1}>{detail.mode==='add'?'添加国标设备':detail.mode==='onvif'?(detail.kind==='camera'?'接入 ONVIF 摄像头':'接入 ONVIF 录像机'):detail.mode==='edit'?'编辑设备':detail.mode==='local'?(localDetail?.name??'设备通道'):'接入详情'}</h2>
          <button type="button" onClick={()=>dialog.current?.close()}>关闭</button>
        </div>
        {notice&&<p className="device-notice" role="status">{notice}</p>}
        {detail.mode==='add'&&<><p>先在 ViewHub 登记设备，再到设备管理页填写接入参数。</p><DeviceForm key="add" onSaved={saved} onCancel={()=>dialog.current?.close()}/></>}
        {detail.mode==='onvif'&&<RecorderSetup kind={detail.kind} onCancel={()=>dialog.current?.close()} onSaved={count=>{setResult('已接入 '+count+' 路通道。');dialog.current?.close();onRefresh()}}/>}
        {detail.mode==='edit'&&current&&<DeviceForm key={current.id} device={current} onSaved={saved} onCancel={()=>open({mode:'setup',id:current.id})}/>}
        {detail.mode==='setup'&&(current&&inventory?<SetupPanel key={current.id} device={current} platform={inventory} onEdit={()=>open({mode:'edit',id:current.id})}/>:<p role="status">正在读取设备配置…</p>)}
        {detail.mode==='local'&&localDetail&&<>
          {localDetail.channels.length?<ul className="device-channels">{localDetail.channels.map(c=><li key={c.id}><strong>{c.name}</strong><span>{c.recordingOnly?'仅录像':c.history?'实时与录像':'实时'}{c.ptz?' · 云台':''}</span>{c.managedRecorder&&<button type="button" disabled={removing||!localDetail.site.online} onClick={()=>void removeRecorder(localDetail.site.id,c.id)}>移除通道</button>}</li>)}</ul>:<p>尚未收到通道信息。</p>}
          {removeError&&<p className="vh-error" role="alert">{removeError}</p>}
        </>}
      </>}
    </dialog>
  </section>;
}

