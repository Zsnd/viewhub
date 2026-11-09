import {useState,type FormEvent} from 'react';
import {api} from './accounts';

type Recording={token:string;source:{name:string;id:string};tracks:{token:string;description:string}[];liveProfiles?:{token:string;name:string}[];autoLive?:boolean};
type Selection={include:boolean;recording:string;track:string;liveProfile:string;name:string};

type CameraProfile={token:string;name:string;ptz:boolean};

export function RecorderSetup({kind,onSaved,onCancel}:{kind:'camera'|'recorder';onSaved:(count:number)=>void;onCancel:()=>void}){
  const camera=kind==='camera';
  const device=camera?'摄像头':'录像机';
  const [profiles,setProfiles]=useState<CameraProfile[]>();
  const [profile,setProfile]=useState('');
  const [rows,setRows]=useState<Recording[]>();
  const [selections,setSelections]=useState<Selection[]>([]);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState('');
  const selected=selections.filter(row=>row.include);

  function reset(){
    setProfiles(undefined);setProfile('');
    setRows(undefined);
    setSelections([]);
    setError('');
  }

  function change(index:number,fields:Partial<Selection>){
    setSelections(list=>list.map((row,i)=>i===index?{...row,...fields}:row));
  }

  async function submit(event:FormEvent<HTMLFormElement>){
    event.preventDefault();
    const fields=Object.fromEntries(new FormData(event.currentTarget));
    setError('');
    if(rows?.length&&(selected.length===0||selected.length>32)){
      setError('请选择 1 至 32 路通道。');return;
    }
    setBusy(true);
    const endpoint='/api/edge/onvif';
    const connection={...fields,kind,port:Number(fields.port),https:fields.https==='on'};
    try{
      const signal=AbortSignal.timeout(28000);
      if(camera){
        if(profile){
          const saved=await api<{added:number}>(endpoint,{method:'PUT',body:JSON.stringify({...connection,liveProfile:profile}),signal});
          onSaved(saved.added);
        }else{
          const result=await api<CameraProfile[]>(endpoint,{method:'POST',body:JSON.stringify(connection),signal});
          setProfiles(result);setProfile(result[0]?.token??'');
        }
      }else if(rows?.length){
        const channels=selected.map(({recording,track,liveProfile,name})=>({recording,track,liveProfile,name}));
        const saved=await api<{added:number}>(endpoint,{method:'PUT',body:JSON.stringify({...connection,channels}),signal});
        onSaved(saved.added);
      }else{
        const result=await api<Recording[]>(endpoint,{method:'POST',body:JSON.stringify(connection),signal});
        setRows(result);
        setSelections(result.map((row,index)=>({
          include:index<32,recording:row.token,track:row.tracks[0].token,
          liveProfile:row.autoLive?row.liveProfiles?.[0]?.token??'':'',
          name:(row.source.name||'通道 '+(index+1)).slice(0,60),
        })));
      }
    }catch(error){
      setError((error as Error).name==='TimeoutError'?'现场设备响应超时，请检查网络后重试。':(error as Error).message);
    }finally{setBusy(false)}
  }

  return <form className="device-form recorder-form" onSubmit={submit}>
    <fieldset className="recorder-fields" disabled={busy} onChange={reset}>
      <legend>{device}信息</legend>
      <label>{device}名称<input name="recorderName" required maxLength={60} placeholder={'例如 仓库'+device} /></label>
      <label>局域网地址<input name="ip" required maxLength={15} placeholder="例如 10.88.1.100" /></label>
      <label>ONVIF 用户名<input name="username" required maxLength={128} autoComplete="off" /></label>
      <label>ONVIF 密码<input name="password" required maxLength={256} type="password" autoComplete="new-password" /></label>
      <details className="recorder-advanced">
        <summary>连接选项</summary>
        <label>ONVIF 端口<input name="port" type="number" required min={1} max={65535} defaultValue={80} /></label>
        <label className="recorder-checkbox"><input name="https" type="checkbox" />使用 HTTPS</label>
      </details>
    </fieldset>
    {!!profiles?.length&&<label>实时视频<select value={profile} onChange={event=>setProfile(event.target.value)}>
      {profiles.map(item=><option key={item.token} value={item.token}>{item.name||item.token}{item.ptz?' · 云台':''}</option>)}
    </select></label>}
    {rows?.length===0&&<p role="status">已连接录像机，未检测到视频录像通道。</p>}
    {!!rows?.length&&<fieldset className="recorder-channel-picker" disabled={busy}>
      <legend>选择通道 · 已选 {selected.length} 路</legend>
      <ul>
        {rows.map((row,index)=>{
          const item=selections[index];
          if(!item)return null;
          const profiles=row.liveProfiles??[];
          const label=item.name;
          return <li key={row.token}>
            <label className="recorder-checkbox">
              <input type="checkbox" checked={item.include} onChange={event=>change(index,{include:event.target.checked})} />
              <strong>{label}</strong>
            </label>
            <span>{item.liveProfile?'实时与录像':'仅录像'}</span>
            {item.include&&(profiles.length>1||(!row.autoLive&&profiles.length>0)||row.tracks.length>1)&&
              <details className="recorder-advanced"><summary>通道选项</summary>
                {profiles.length>0&&<label>实时视频<select value={item.liveProfile} onChange={event=>change(index,{liveProfile:event.target.value})}>
                  <option value="">仅录像</option>
                  {profiles.map(profile=><option key={profile.token} value={profile.token}>{profile.name||profile.token}</option>)}
                </select></label>}
                {row.tracks.length>1&&<label>录像轨道<select value={item.track} onChange={event=>change(index,{track:event.target.value})}>
                  {row.tracks.map(track=><option key={track.token} value={track.token}>{track.description||track.token}</option>)}
                </select></label>}
              </details>}
          </li>;
        })}
      </ul>
    </fieldset>}
    {error&&<p className="vh-error" role="alert">{error}</p>}
    <div className="vh-actions">
      <button type="submit" className="primary" disabled={busy||!!rows?.length&&!selected.length}>
        {busy?(rows?.length||profile?'正在接入…':'正在检测…'):(rows?.length||profile?'接入'+device:'检测通道')}
      </button>
      <button type="button" onClick={onCancel} disabled={busy}>取消</button>
    </div>
  </form>;
}

