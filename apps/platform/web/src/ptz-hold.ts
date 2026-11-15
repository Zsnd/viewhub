import {useEffect,useRef,useState} from 'react';
import {api} from './accounts';

/** Ordered commands plus a short server lease prevent motion surviving a lost page. */
export function usePtzHold(base:string,enabled:boolean,onError:(message:string)=>void){
 const [pressed,setPressed]=useState('');
 const controller=useRef<{start:(action:string)=>void;stop:()=>void}>({start:()=>{},stop:()=>{}});
 useEffect(()=>{
  let active='',disposed=false,pending=Promise.resolve();
  let timer:ReturnType<typeof setTimeout>|undefined;
  function send(action:string,hold=false){
   const result=pending.then(()=>api(base+'/ptz',{method:'POST',body:JSON.stringify({action,hold}),keepalive:true}));
   pending=result.then(()=>{},()=>{});return result;
  }
  function stop(){if(!active)return;active='';clearTimeout(timer);if(!disposed)setPressed('');void send('stop').catch(error=>{if(!disposed)onError(error.message)})}
  function renew(action:string){void send(action,true).then(()=>{if(!disposed&&active===action)timer=setTimeout(()=>renew(action),250)}).catch(error=>{stop();if(!disposed)onError(error.message)})}
  function start(action:string){if(!enabled||disposed||active)return;active=action;setPressed(action);renew(action)}
  const hidden=()=>{if(document.hidden)stop()};
  controller.current={start,stop};setPressed('');
  window.addEventListener('pointerup',stop);window.addEventListener('pointercancel',stop);window.addEventListener('blur',stop);window.addEventListener('pagehide',stop);document.addEventListener('visibilitychange',hidden);
  return()=>{stop();disposed=true;window.removeEventListener('pointerup',stop);window.removeEventListener('pointercancel',stop);window.removeEventListener('blur',stop);window.removeEventListener('pagehide',stop);document.removeEventListener('visibilitychange',hidden)};
 },[base,enabled,onError]);
 return {pressed,start:(action:string)=>controller.current.start(action),stop:()=>controller.current.stop()};
}
