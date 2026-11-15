import {useEffect,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Login,api,type Account} from './accounts';
import {Workspace} from './workspace';
import '@fontsource-variable/public-sans';
import './viewhub.css';
function App(){
  const [account,setAccount]=useState<Account>();
  const [loading,setLoading]=useState(true);
  const [message,setMessage]=useState('');
  useEffect(()=>{
    void api<Account>('/api/auth/me').then(setAccount).catch(()=>{}).finally(()=>setLoading(false));
    const expired=()=>{setAccount(undefined);setMessage('登录已过期，请重新登录。')};
    window.addEventListener('login-expired',expired);
    return ()=>window.removeEventListener('login-expired',expired);
  },[]);
  if(loading)return <main className="app-loading"><p role="status">正在读取登录状态…</p></main>;
  if(!account||account.mustChange)return <Login key={account?.username??'login'} account={account} message={message} onLogin={next=>{
    if(!next&&account?.mustChange)setMessage('请使用当前密码登录。');
    setAccount(next);
  }}/>;
  return <Workspace key={account.id} account={account}
    onLogout={()=>{setAccount(undefined);setMessage('已退出登录。')}}
    onPasswordChanged={()=>{setAccount(undefined);setMessage('密码已修改，请用新密码重新登录。')}}/>;
}
createRoot(document.getElementById('root')!).render(<App/>);
