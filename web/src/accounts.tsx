import {useEffect,useRef,useState,type FormEvent} from 'react';
import {Icon} from './icons';

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

export function Login({account,onLogin,message}:{account?:Account;onLogin:(account?:Account)=>void;message:string}){
  const [error,setError]=useState('');
  const [busy,setBusy]=useState(false);
  const change=account?.mustChange===true;
  const [showPassword,setShowPassword]=useState(false);
  async function submit(event:FormEvent<HTMLFormElement>){
    event.preventDefault();setError('');setBusy(true);
    const fields=Object.fromEntries(new FormData(event.currentTarget));
    try{
      if(change){
        if(fields.password!==fields.confirm)throw new Error('两次输入的新密码不一致');
        await api('/api/auth/password',{method:'POST',body:JSON.stringify(fields)});
        onLogin();
      }else{
        onLogin(await api<Account>('/api/auth/login',{method:'POST',body:JSON.stringify(fields)}));
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
        {change?<label>初始密码<input name="current" type="password" autoComplete="current-password" required/></label>
        :<label>登录账号<input name="username" autoComplete="username" required/></label>}
        <div className="password-field"><label htmlFor="login-password">{change?'新密码':'密码'}</label><div className="password-entry"><input id="login-password" name="password" type={showPassword?'text':'password'} autoComplete={change?'new-password':'current-password'} minLength={change?12:undefined} required/><button type="button" aria-controls="login-password" aria-pressed={showPassword} onClick={()=>setShowPassword(!showPassword)}>{showPassword?'隐藏':'显示'}</button></div></div>
        {change&&<label>确认新密码<input name="confirm" type="password" autoComplete="new-password" minLength={12} required/></label>}
        <button className="primary" disabled={busy}>{change?'保存新密码':'登录'}</button>
      </form>
      {change&&<button className="auth-back" type="button" disabled={busy} onClick={()=>void api('/api/auth/logout',{method:'POST'}).then(()=>onLogin()).catch(e=>setError(e.message))}>返回登录</button>}
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
      <button className="primary" aria-expanded={creating} onClick={()=>{setError('');setCreating(!creating)}}>{creating?'收起表单':'创建账号'}</button>
    </div>
    {creating&&<form className="account-create" onSubmit={create}>
      <h2>创建客户账号</h2>
      <label>登录账号<input ref={username} name="username" required minLength={3} maxLength={40} pattern="[a-zA-Z0-9][a-zA-Z0-9._\-]{2,39}" autoComplete="off"/></label>
      <label>客户空间<input name="space" required maxLength={50}/></label>
      <label>初始密码<input name="password" type="password" required minLength={12} maxLength={72} autoComplete="new-password"/></label>
      <button className="primary" disabled={busy}>创建账号</button>
    </form>}
    {error&&<p role="alert" className="error-text">{error}</p>}
    {result&&<p role="status" className="device-notice">{result}</p>}
    <div className="account-roster">
      <div className="registry-toolbar"><h2>空间名册 <small>{accounts.length}</small></h2>
        <input type="search" aria-label="搜索空间或账号" placeholder="搜索空间或账号" value={query} onChange={e=>setQuery(e.target.value)}/>
      </div>
      {!visible.length&&<p className="vh-empty">{accounts.length?'没有匹配的空间。':'正在读取账号…'}</p>}
      <ul className="account-list">{visible.map(account=><li key={account.id}>
        <div><strong>{account.space}</strong><small>{account.username}</small></div>
        <span>{account.admin?'系统管理员':account.mustChange?'待修改初始密码':'客户账号'}</span>
      </li>)}</ul>
    </div>
  </section>;
}

