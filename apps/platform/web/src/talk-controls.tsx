import {useEffect,useRef,useState} from 'react';
import {api,headers} from './accounts';
import {Button} from '@/components/ui/button';
import {Mic,MicOff,PhoneOff,LoaderCircle,Volume2,VolumeX} from 'lucide-react';

type Phase='idle'|'permission'|'connecting'|'connected';
type Resources={pc?:RTCPeerConnection;stream?:MediaStream;id?:string;abort:AbortController;heartbeat?:ReturnType<typeof setInterval>;disconnect?:ReturnType<typeof setTimeout>};

export function TalkControls({base,online,onStart,disabledReason}:{base:string;online:boolean;onStart:()=>void;disabledReason?:string}){
 const [phase,setPhase]=useState<Phase>('idle');
 const [error,setError]=useState('');
 const [muted,setMuted]=useState(false);
 const [seconds,setSeconds]=useState(0);
 const [receiving,setReceiving]=useState(false);
 const [listening,setListening]=useState(true);
 const audio=useRef<HTMLAudioElement>(null);
 const current=useRef<Resources|undefined>(undefined);
 function release(resource?:Resources){
  if(!resource)return;
  resource.abort.abort();clearInterval(resource.heartbeat);clearTimeout(resource.disconnect);
  if(resource.pc){resource.pc.onconnectionstatechange=null;resource.pc.close()}
  resource.stream?.getTracks().forEach(track=>track.stop());
  if(resource.id)void fetch('/api/talk/'+resource.id,{method:'DELETE',headers,keepalive:true}).catch(()=>{});
  if(current.current===resource&&audio.current){audio.current.pause();audio.current.srcObject=null}
  if(current.current===resource)current.current=undefined;
 }
 function stop(){release(current.current);setPhase('idle');setMuted(false);setSeconds(0);setReceiving(false)}
 useEffect(()=>{const close=()=>release(current.current);window.addEventListener('pagehide',close);return()=>{window.removeEventListener('pagehide',close);close()}},[base]);
 useEffect(()=>{if(!online)stop()},[online]);
 useEffect(()=>{if(phase!=='connected')return;const timer=setInterval(()=>setSeconds(value=>value+1),1000);return()=>clearInterval(timer)},[phase]);
 async function start(){
  if(current.current)return;
  setError('');setMuted(false);setSeconds(0);setReceiving(false);setListening(true);setPhase('permission');
  const resource:Resources={abort:new AbortController()};current.current=resource;
  try{
   if(!navigator.mediaDevices?.getUserMedia)throw new Error('请通过 HTTPS 访问，并使用支持麦克风的浏览器。');
   const stream=await navigator.mediaDevices.getUserMedia({audio:{echoCancellation:true,noiseSuppression:true,autoGainControl:true},video:false});
   if(current.current!==resource){stream.getTracks().forEach(track=>track.stop());return}
   resource.stream=stream;
   stream.getAudioTracks().forEach(track=>{track.onended=()=>{if(current.current===resource){stop();setError('麦克风已断开，请检查输入设备后重试。')}}});
   setPhase('connecting');
   const pc=new RTCPeerConnection();resource.pc=pc;
   pc.ontrack=event=>{
    if(current.current!==resource||event.track.kind!=='audio')return;
    if(audio.current){audio.current.srcObject=new MediaStream([event.track]);audio.current.muted=false;void audio.current.play().catch(()=>setListening(false))}
    event.track.onunmute=()=>{if(current.current===resource)setReceiving(true)};
   };
   for(const track of stream.getAudioTracks())pc.addTrack(track,stream);
   const codecs=RTCRtpSender.getCapabilities('audio')?.codecs.filter(c=>c.mimeType.toLowerCase()==='audio/pcma')??[];
   if(!codecs.length)throw new Error('当前浏览器不支持设备所需的 G.711A 音频编码。');
   for(const transceiver of pc.getTransceivers())transceiver.setCodecPreferences(codecs);
   pc.onconnectionstatechange=()=>{
    if(current.current!==resource)return;
    clearTimeout(resource.disconnect);
    if(pc.connectionState==='connected'){setPhase('connected');onStart()}
    if(pc.connectionState==='failed'){stop();setError('音频连接失败，请检查网络后重试。')}
    if(pc.connectionState==='disconnected')resource.disconnect=setTimeout(()=>{if(current.current===resource){stop();setError('音频连接已断开。')}},4000);
   };
   await pc.setLocalDescription(await pc.createOffer());
   await new Promise<void>(resolve=>{
    const finish=()=>{clearTimeout(timer);pc.removeEventListener('icegatheringstatechange',changed);resolve()};
    const changed=()=>{if(pc.iceGatheringState==='complete')finish()};
    const timer=setTimeout(finish,1000);pc.addEventListener('icegatheringstatechange',changed);changed();
   });
   if(current.current!==resource)return;
   const response=await fetch(base+'/talk',{method:'POST',headers:{...headers,'Content-Type':'application/sdp'},body:pc.localDescription!.sdp,signal:AbortSignal.any([resource.abort.signal,AbortSignal.timeout(18000)])});
   const result=await response.json();
   if(!response.ok){if(response.status===401)window.dispatchEvent(new Event('login-expired'));throw new Error(result.error??'设备未能建立语音连接。')}
   resource.id=result.id;
   if(current.current!==resource){release(resource);return}
   await pc.setRemoteDescription({type:'answer',sdp:result.sdp});
   resource.heartbeat=setInterval(()=>{void api('/api/talk/'+result.id+'/heartbeat',{method:'POST'}).catch(e=>{if(current.current===resource){stop();setError(e.message)}})},4000);
   resource.disconnect=setTimeout(()=>{if(current.current===resource&&pc.connectionState!=='connected'){stop();setError('等待浏览器音频连接超时，请重试。')}},10000);
  }catch(e){
   if(current.current!==resource)return;
   const problem=e as Error;stop();setError(problem.name==='NotAllowedError'?'麦克风权限未开启，请在浏览器地址栏允许麦克风后重试。':problem.name==='NotFoundError'?'未找到麦克风，请连接输入设备后重试。':problem.message||'对讲连接失败，请重试。');
  }
 }
 function toggleMute(){const next=!muted;current.current?.stream?.getAudioTracks().forEach(track=>{track.enabled=!next});setMuted(next)}
 function toggleListening(){const next=!listening;setListening(next);if(audio.current){audio.current.muted=!next;if(next)void audio.current.play().catch(()=>{setListening(false);setError('浏览器未能播放现场声音，请重试。')})}}
 return <section className="talk-controls" aria-label="语音对讲"><div className="talk-heading"><h3>语音对讲</h3>{phase==='connected'&&<span role="status" className="talk-active">{muted?'麦克风已静音':'麦克风正在发送'} · {Math.floor(seconds/60)}:{String(seconds%60).padStart(2,'0')}</span>}</div>
 <audio ref={audio} autoPlay/>
 {phase==='idle'?<><p>{disabledReason??'开启麦克风向现场讲话，同时接收设备回传的声音。'}</p><Button variant="outline" size="sm" disabled={!online} onClick={()=>void start()}><Mic size={15}/>开始对讲</Button></>:phase==='connected'?<><p role="status">{receiving?'正在接收现场声音':'尚未收到现场声音，请确认设备开启了音频采集。'}</p><div className="vh-actions"><Button variant={muted?'secondary':'outline'} size="sm" onClick={toggleMute}>{muted?<MicOff size={15}/>:<Mic size={15}/>} {muted?'开启麦克风':'静音'}</Button><Button variant="outline" size="sm" onClick={toggleListening}>{listening?<Volume2 size={15}/>:<VolumeX size={15}/>} {listening?'关闭现场声音':'开启现场声音'}</Button><Button variant="destructive" size="sm" onClick={stop}><PhoneOff size={15}/>结束对讲</Button></div></>:<div className="vh-actions"><span role="status" className="talk-pending"><LoaderCircle size={15} className="animate-spin"/>{phase==='permission'?'等待麦克风权限…':'正在连接设备…'}</span><Button variant="outline" size="sm" onClick={stop}>取消</Button></div>}
 {error&&<p role="alert" className="vh-error">{error}</p>}
 </section>;
}
