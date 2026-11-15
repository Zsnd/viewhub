import {useState,useRef,type FormEvent} from 'react';
import {api} from './accounts';
import {SetupPanel,type Device,type Platform} from './onboarding';
import {Button} from '@/components/ui/button';
import {Input} from '@/components/ui/input';
import {Badge} from '@/components/ui/badge';
import {Dialog,DialogContent,DialogDescription,DialogHeader,DialogTitle,DialogFooter} from '@/components/ui/dialog';
import {AlertDialog,AlertDialogContent,AlertDialogHeader,AlertDialogTitle,AlertDialogDescription,AlertDialogFooter,AlertDialogCancel} from '@/components/ui/alert-dialog';
import {DropdownMenu,DropdownMenuTrigger,DropdownMenuContent,DropdownMenuItem,DropdownMenuSeparator} from '@/components/ui/dropdown-menu';
import {Table,TableHeader,TableRow,TableHead,TableBody,TableCell} from '@/components/ui/table';
import {Plus,Search,MoreHorizontal,ArrowLeft,Camera,Check,ChevronRight,Trash2,Settings2,Pencil,Video,RefreshCw} from 'lucide-react';

type Inventory=Platform&{devices:Device[]};
const kindName=(kind:string)=>kind==='recorder'?'录像机':'摄像头';
function state(device:Device){return device.online?'在线':device.diagnostics.registration==='offline'?'离线':device.diagnostics.failure&&device.diagnostics.failure!=='challenge'?'注册失败':'等待注册'}

function DeviceForm({device,onSaved,onCancel,onBusy}:{device?:Device;onSaved:(id:string)=>void;onCancel:()=>void;onBusy:(busy:boolean)=>void}){
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 async function save(event:FormEvent<HTMLFormElement>){
  event.preventDefault();if(busy)return;setBusy(true);onBusy(true);setError('');
  const data=Object.fromEntries(new FormData(event.currentTarget));
  try{await api(device?'/api/devices/'+device.id:'/api/devices',{method:device?'PUT':'POST',body:JSON.stringify({...data,name:String(data.name).trim(),id:device?.id??String(data.id).trim()})});onSaved(device?.id??String(data.id).trim())}catch(e){setError((e as Error).message)}finally{setBusy(false);onBusy(false)}
 }
 return <form className="device-form" onSubmit={save}>
  <label>设备名称<Input autoFocus name="name" required maxLength={100} defaultValue={device?.name} placeholder="例如：仓库入口"/></label>
  <label>设备类型<select name="kind" defaultValue={device?.kind??'camera'}><option value="camera">摄像头</option><option value="recorder">录像机</option></select></label>
  {device?<div className="readonly-field"><span>设备编码</span><code>{device.id}</code><small>设备编码用于识别设备，添加后不能修改。</small></div>:<label>设备编码<Input name="id" required pattern="[0-9]{20}" inputMode="numeric" maxLength={20} placeholder="20 位 SIP 用户 ID" aria-describedby="device-id-hint"/><small id="device-id-hint">与现场设备平台接入中的 SIP 用户名保持一致。</small></label>}
  <label>{device?'新国标注册密码（选填）':'国标注册密码'}<Input name="secret" type="password" required={!device} minLength={8} maxLength={64} autoComplete="new-password" aria-describedby="device-secret-hint"/><small id="device-secret-hint">{device?'留空保留原密码。修改后，请同步更新现场设备的国标注册密码。':'设置 8–64 位密码，稍后填入现场设备。这不是摄像头管理页的登录密码。'}</small></label>
  {error&&<p className="vh-error" role="alert">{error}</p>}
  <DialogFooter><Button type="button" variant="outline" disabled={busy} onClick={onCancel}>取消</Button><Button type="submit" disabled={busy}>{busy?'正在保存…':device?'保存修改':'添加并配置接入'}</Button></DialogFooter>
 </form>;
}

export function DeviceSettings({inventory,onRefresh,onWatch}:{inventory?:Inventory;onRefresh:()=>void;onWatch?:(key:string)=>void}){
 const [form,setFormState]=useState<'add'|Device>();
 const lastForm=useRef<'add'|Device|undefined>(undefined);
 const [formBusy,setFormBusy]=useState(false);
 function setForm(value:'add'|Device|undefined){if(value)lastForm.current=value;setFormState(value)}
 const shownForm=form??lastForm.current;
 const lastRemoval=useRef<Device|undefined>(undefined);
 const [detail,setDetail]=useState('');
 const [added,setAdded]=useState('');
 const [removing,setRemoving]=useState<Device>();
 const [busy,setBusy]=useState(false);
 const [deleteError,setDeleteError]=useState('');
 const [notice,setNotice]=useState('');
 const [query,setQuery]=useState('');
 const [filter,setFilter]=useState('all');
 const devices=inventory?.devices??[];
 const current=devices.find(d=>d.id===detail);
 const term=query.trim().toLowerCase();
 const visible=devices.filter(d=>[d.name,d.id,kindName(d.kind)].join(' ').toLowerCase().includes(term)&&(filter==='all'||(filter==='online'?d.online:!d.online)));
 function saved(id:string){setNotice(form==='add'?'设备已登记，请继续填写现场接入参数。':'设备修改已保存。');if(form==='add'){setAdded(id);setDetail(id)}setForm(undefined);onRefresh()}
 async function remove(){
  if(!removing||busy)return;setBusy(true);setDeleteError('');
  try{await api('/api/devices/'+removing.id,{method:'DELETE'});setNotice('已移除“'+removing.name+'”，相关监看会话已结束。');if(detail===removing.id)setDetail('');setRemoving(undefined);onRefresh()}catch(e){setDeleteError((e as Error).message)}finally{setBusy(false)}
 }
 function askRemove(d:Device){lastRemoval.current=d;setDeleteError('');setRemoving(d)}
 const shownRemoval=removing??lastRemoval.current;
 return <section className="device-settings">
 {detail?<><Button className="back-link" variant="ghost" size="sm" onClick={()=>{setDetail('');setAdded('');setNotice('')}}><ArrowLeft size={16}/>返回设备列表</Button><header className="page-heading"><div><h1>{current?.name??'设备详情'}</h1><p>{current?kindName(current.kind)+' · '+current.id:'正在读取接入信息…'}</p></div>{current&&<div className="vh-actions"><Button variant="outline" onClick={()=>setForm(current)}><Pencil size={15}/>编辑</Button><Button variant="outline" onClick={()=>askRemove(current)}><Trash2 size={15}/>移除设备</Button></div>}</header>
 {added===detail&&<ol className="connection-steps" aria-label="设备接入进度"><li data-complete="true"><Check size={16}/>登记设备</li><li data-complete={current?.online??false}><Settings2 size={16}/>配置并注册</li><li data-complete={!!current?.channels.length}><Video size={16}/>发现通道</li></ol>}
 {notice&&<div className="notice-banner" role="status">{notice}</div>}
 {current&&inventory?<div className="device-detail"><SetupPanel key={current.id} device={current} platform={inventory} onEdit={()=>setForm(current)} onRefresh={onRefresh}/>{current.channels.length>0&&<div className="detail-actions"><Button onClick={()=>onWatch?.('gb/'+current.id+'/'+current.channels[0].id)}><Video size={16}/>查看实时画面<ChevronRight size={16}/></Button></div>}</div>:<p role="status">正在同步设备…</p>}</>:
 <><header className="page-heading"><div><h1>设备管理</h1><p>接入摄像头和录像机，检查注册状态与通道能力。</p></div><Button onClick={()=>{setNotice('');setForm('add')}}><Plus size={17}/>添加设备</Button></header>
 {notice&&<div className="notice-banner" role="status">{notice}</div>}
 <div className="device-registry"><div className="registry-toolbar"><div className="search-field"><Search size={16}/><Input type="search" aria-label="搜索设备" placeholder="搜索名称或设备编码" value={query} onChange={e=>setQuery(e.target.value)}/></div><div className="registry-filters"><select aria-label="筛选设备状态" value={filter} onChange={e=>setFilter(e.target.value)}><option value="all">全部状态</option><option value="online">在线</option><option value="offline">未在线</option></select><Button variant="outline" size="icon" aria-label="刷新设备" onClick={onRefresh}><RefreshCw size={16}/></Button></div></div>
 <Table><TableHeader><TableRow><TableHead>设备名称</TableHead><TableHead>类型</TableHead><TableHead>注册状态</TableHead><TableHead>通道数</TableHead><TableHead>最近有效信令</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader><TableBody>{visible.map(device=><TableRow key={device.id}><TableCell><button className="device-name" onClick={()=>{setDetail(device.id);setNotice('')}}><span className="device-icon"><Camera size={19}/></span><span><strong>{device.name}</strong><small>{device.id}</small></span></button></TableCell><TableCell>{kindName(device.kind)}</TableCell><TableCell><Badge className="status-badge" data-online={device.online} variant="secondary"><span className="signal-dot" data-online={device.online}/>{state(device)}</Badge></TableCell><TableCell>{device.channels.length} 路</TableCell><TableCell className="muted">{device.diagnostics.lastSignal?new Date(device.diagnostics.lastSignal).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}):'尚无信令'}</TableCell><TableCell className="text-right"><DropdownMenu><DropdownMenuTrigger asChild><Button variant="ghost" size="icon" aria-label={device.name+' 更多操作'}><MoreHorizontal size={18}/></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem onSelect={()=>{setDetail(device.id);setNotice('')}}><Settings2 size={15}/>接入详情</DropdownMenuItem><DropdownMenuItem onSelect={()=>setForm(device)}><Pencil size={15}/>编辑设备</DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem className="text-destructive" onSelect={()=>askRemove(device)}><Trash2 size={15}/>移除设备</DropdownMenuItem></DropdownMenuContent></DropdownMenu></TableCell></TableRow>)}</TableBody></Table>
 {!visible.length&&<div className="workspace-empty registry-empty"><Camera size={34}/><h2>{!inventory?'正在读取设备…':devices.length?'没有匹配的设备':'接入第一台设备'}</h2><p>{devices.length?'尝试其他名称、编码或状态。':'添加设备后，按照接入参数配置现场设备。'}</p>{inventory&&!devices.length&&<Button onClick={()=>setForm('add')}><Plus size={16}/>添加设备</Button>}</div>}
 <footer className="registry-footer">共 {visible.length} 台设备{devices.length!==visible.length?' · 总计 '+devices.length+' 台':''}</footer></div></>}
 <Dialog open={form!==undefined} onOpenChange={open=>{if(!open&&!formBusy)setForm(undefined)}}><DialogContent showCloseButton={!formBusy} className="device-form-dialog"><DialogHeader><DialogTitle>{shownForm==='add'?'添加国标设备':'编辑设备'}</DialogTitle><DialogDescription>{shownForm==='add'?'先登记设备，再按接入参数配置现场设备。':'修改设备信息；注册密码留空则保持原密码。'}</DialogDescription></DialogHeader>{shownForm&&<DeviceForm key={typeof shownForm==='string'?'add':shownForm.id} device={typeof shownForm==='string'?undefined:shownForm} onBusy={setFormBusy} onSaved={saved} onCancel={()=>setForm(undefined)}/>}</DialogContent></Dialog>
 <AlertDialog open={!!removing} onOpenChange={open=>{if(!open&&!busy)setRemoving(undefined)}}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>移除“{shownRemoval?.name}”？</AlertDialogTitle><AlertDialogDescription>设备编码 {shownRemoval?.id}。移除后会结束相关监看会话，并清空监控墙中的关联。现场设备上的录像会保留；重新接入需要再次添加设备。</AlertDialogDescription></AlertDialogHeader>{deleteError&&<p role="alert" className="vh-error">{deleteError}</p>}<AlertDialogFooter><AlertDialogCancel disabled={busy}>取消</AlertDialogCancel><Button variant="destructive" disabled={busy} onClick={()=>void remove()}>{busy?'正在移除…':'确认移除'}</Button></AlertDialogFooter></AlertDialogContent></AlertDialog>
 </section>;
}
