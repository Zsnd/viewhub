import {useEffect,useRef,useState,type FormEvent} from 'react';
import {Icon} from './icons';
import {Button} from '@/components/ui/button';
import {Input} from '@/components/ui/input';
import {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter} from '@/components/ui/dialog';

export type Account={id:number;username:string;space:string;admin:boolean;mustChange:boolean};
export const headers={'X-Playback-Request':'1','Content-Type':'application/json'};

export async function api<T>(url:string,options:RequestInit={}):Promise<T>{
  const response=await fetch(url,{...options,headers,signal:options.signal??AbortSignal.timeout(12000)});
  if(!response.ok){
    if(response.status===401&&url!=='/api/auth/login'&&url!=='/api/auth/me'){
      window.dispatchEvent(new Event('login-expired'));
    }
    const body=await response.json().catch(()=>({}));
    throw new Error(body.error??'请求失败，请重试');
  }
  return response.status===204 ? undefined as T : response.json();
}

async function changePassword(fields:FormData){
  const current=String(fields.get('current')??'');
  const password=String(fields.get('password')??'');
  if(password!==fields.get('confirm'))throw new Error('两次输入的新密码不一致');
  const length=new TextEncoder().encode(password).length;
  if(length<12||length>72||password===current)throw new Error('新密码需为 12–72 字节，且与当前密码不同');
  await api('/api/auth/password',{method:'POST',body:JSON.stringify({current,password})});
}

export function PasswordSettings({onChanged}:{onChanged:()=>void}){
  const [open,setOpen]=useState(false);
  const form=useRef<HTMLFormElement>(null);
  const [error,setError]=useState('');
  const [busy,setBusy]=useState(false);

  async function submit(event:FormEvent<HTMLFormElement>){
    event.preventDefault();
    if(busy)return;
    setError('');
    setBusy(true);
    try{
      await changePassword(new FormData(event.currentTarget));
      onChanged();
    }catch(e){setError((e as Error).message)}
    finally{setBusy(false)}
  }

  return <>
    <Button variant="outline" type="button" className="signout" onClick={()=>setOpen(true)}>修改密码</Button>
    <Dialog open={open} onOpenChange={next=>{if(!busy){setOpen(next);if(!next){form.current?.reset();setError('')}}}}><DialogContent showCloseButton={!busy}>
      <DialogHeader><DialogTitle>修改密码</DialogTitle><DialogDescription>保存后，当前账号的所有登录会失效。请用新密码重新登录。</DialogDescription></DialogHeader>
      <form className="password-settings-form" ref={form} onSubmit={submit}>
        <label>当前密码<Input name="current" type="password" autoComplete="current-password" autoFocus required/></label>
        <label>新密码<Input name="password" type="password" autoComplete="new-password" required/></label>
        <label>确认新密码<Input name="confirm" type="password" autoComplete="new-password" required/></label>
        {error&&<p role="alert" className="vh-error">{error}</p>}
        <div className="vh-actions">
          <Button variant="outline" type="button" disabled={busy} onClick={()=>setOpen(false)}>取消</Button>
          <Button variant="outline" type="submit" className="primary" disabled={busy}>{busy?'正在保存…':'保存新密码'}</Button>
        </div>
      </form>
    </DialogContent></Dialog>
  </>;
}

export function Login({account,onLogin,message}:{account?:Account;onLogin:(account?:Account)=>void;message:string}){
  const [error,setError]=useState('');
  const [busy,setBusy]=useState(false);
  const change=account?.mustChange===true;
  const [showPassword,setShowPassword]=useState(false);
  async function submit(event:FormEvent<HTMLFormElement>){
    event.preventDefault();setError('');setBusy(true);
    const fields=new FormData(event.currentTarget);
    try{
      if(change){
        await changePassword(fields);
        onLogin();
      }else{
        onLogin(await api<Account>('/api/auth/login',{method:'POST',body:JSON.stringify(Object.fromEntries(fields))}));
      }
    }catch(e){setError((e as Error).message)}
    finally{setBusy(false)}
  }
  return <main className="auth-shell">
    <section className="auth-card">
      <div className="auth-identity"><Icon name="brand"/><strong>ViewHub</strong></div>
      <h1>{change?'修改初始密码':'登录'}</h1>
      <p>{change?'为 '+account.username+' 设置新密码，然后重新登录。':message||'登录后查看你所在空间的设备。'}</p>
      <form onSubmit={submit}>
        {change?<label>初始密码<Input name="current" type="password" autoComplete="current-password" required/></label>
        :<label>登录账号<Input name="username" autoComplete="username" required/></label>}
        <div className="password-field"><label htmlFor="login-password">{change?'新密码':'密码'}</label><div className="password-entry"><Input id="login-password" name="password" type={showPassword?'text':'password'} autoComplete={change?'new-password':'current-password'} required/><Button variant="outline" type="button" aria-controls="login-password" aria-pressed={showPassword} onClick={()=>setShowPassword(!showPassword)}>{showPassword?'隐藏':'显示'}</Button></div></div>
        {change&&<label>确认新密码<Input name="confirm" type="password" autoComplete="new-password" required/></label>}
        <Button variant="outline" className="primary" disabled={busy}>{change?'保存新密码':'登录'}</Button>
      </form>
      {change&&<Button variant="outline" className="auth-back" type="button" disabled={busy} onClick={()=>void api('/api/auth/logout',{method:'POST'}).then(()=>onLogin()).catch(e=>setError(e.message))}>返回登录</Button>}
      {error&&<p role="alert" className="error-text">{error}</p>}
    </section>
  </main>;
}

export function Accounts(){
  const [accounts,setAccounts]=useState<Account[]>([]);
  const [error,setError]=useState('');
  const [result,setResult]=useState('');
  const [busy,setBusy]=useState(false);
  const [creating,setCreating]=useState(false);
  const [query,setQuery]=useState('');
  const username=useRef<HTMLInputElement>(null);
  useEffect(()=>{if(creating)username.current?.focus()},[creating]);
  const refresh=()=>api<Account[]>('/api/admin/accounts').then(setAccounts).catch(e=>setError(e.message));
  useEffect(()=>{void refresh()},[]);
  async function create(event:FormEvent<HTMLFormElement>){
    event.preventDefault();setError('');setResult('');setBusy(true);
    const form=event.currentTarget;
    const fields=Object.fromEntries(new FormData(form));
    try{
      const created=await api<Account>('/api/admin/accounts',{method:'POST',body:JSON.stringify(fields)});
      setResult('已创建 '+created.username+'。请将初始密码交给客户，首次登录时需修改。');
      form.reset();setCreating(false);setQuery('');await refresh();
    }catch(e){setError((e as Error).message)}
    finally{setBusy(false)}
  }
  const term=query.trim().toLocaleLowerCase();
  const visible=accounts.filter(account=>(account.username+' '+account.space).toLocaleLowerCase().includes(term));
  return <section className="accounts-panel">
    <div className="vh-heading"><p>每个客户账号拥有独立的设备与监控墙。</p>
      <Button variant="outline" className="primary" aria-expanded={creating} onClick={()=>{setError('');setCreating(!creating)}}>{creating?'收起表单':'创建账号'}</Button>
    </div>
    {creating&&<form className="account-create" onSubmit={create}>
      <h2>创建客户账号</h2>
      <label>登录账号<Input ref={username} name="username" required minLength={3} maxLength={40} pattern="[a-zA-Z0-9][a-zA-Z0-9._\-]{2,39}" autoComplete="off"/></label>
      <label>客户空间<Input name="space" required maxLength={50}/></label>
      <label>初始密码<Input name="password" type="password" required minLength={12} maxLength={72} autoComplete="new-password"/></label>
      <Button variant="outline" className="primary" disabled={busy}>创建账号</Button>
    </form>}
    {error&&<p role="alert" className="error-text">{error}</p>}
    {result&&<p role="status" className="device-notice">{result}</p>}
    <div className="account-roster">
      <div className="registry-toolbar"><h2>空间名册 <small>{accounts.length}</small></h2>
        <Input type="search" aria-label="搜索空间或账号" placeholder="搜索空间或账号" value={query} onChange={e=>setQuery(e.target.value)}/>
      </div>
      {!visible.length&&<p className="vh-empty">{accounts.length?'没有匹配的空间。':'正在读取账号…'}</p>}
      <ul className="account-list">{visible.map(account=><li key={account.id}>
        <div><strong>{account.space}</strong><small>{account.username}</small></div>
        <span>{account.admin?'系统管理员':account.mustChange?'待修改初始密码':'客户账号'}</span>
      </li>)}</ul>
    </div>
  </section>;
}

