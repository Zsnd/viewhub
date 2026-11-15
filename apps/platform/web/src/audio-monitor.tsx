import {useEffect,useRef} from 'react';
import {headers} from './accounts';

/** Receive-only PCMA, sharing the current viewer lease and camera media feed. */
export function AudioMonitor({endpoint,enabled,volume=1,onStatus}:{endpoint:string;enabled:boolean;volume?:number;onStatus?:(status:string)=>void}){
 const audio=useRef<HTMLAudioElement>(null);
 useEffect(()=>{if(audio.current)audio.current.volume=volume},[volume]);
 useEffect(()=>{
  const element=audio.current!;
  element.dataset.audioPackets='0';element.dataset.audioEnergy='0';element.dataset.audioState='off';
  if(!enabled){onStatus?.('');return}
  if(!window.RTCPeerConnection){onStatus?.('浏览器不支持音频连接');return}
  const abort=new AbortController();const pc=new RTCPeerConnection();
  let resourceURL='';
  let disposed=false,received=false,lastPackets=0,emptySince=Date.now();
  let deadline:ReturnType<typeof setTimeout>,stats:ReturnType<typeof setInterval>;
  function close(){abort.abort();clearTimeout(deadline);clearInterval(stats);pc.close();element.pause();element.srcObject=null;if(resourceURL){void fetch(resourceURL,{method:'DELETE',headers,keepalive:true}).catch(()=>{});resourceURL=''}}
  function fail(message:string){if(disposed)return;disposed=true;close();element.dataset.audioState='error';onStatus?.(message)}
  element.dataset.audioState='connecting';onStatus?.('正在连接现场声音…');
  const transceiver=pc.addTransceiver('audio',{direction:'recvonly'});
  const codecs=RTCRtpReceiver.getCapabilities('audio')?.codecs.filter(codec=>codec.mimeType.toLowerCase()==='audio/pcma')??[];
  if(!codecs.length){fail('浏览器不支持现场音频编码');return close}
  transceiver.setCodecPreferences(codecs);
  pc.ontrack=event=>{
   if(disposed)return;
   element.srcObject=new MediaStream([event.track]);
   void element.play().catch(()=>fail('浏览器未允许播放声音，请关闭声音后重试'));
   element.muted=false;
  };
  pc.onconnectionstatechange=()=>{if(!disposed&&pc.connectionState==='failed')fail('现场声音连接失败，请关闭声音后重试')};
  deadline=setTimeout(()=>fail(lastPackets>0?'浏览器未允许播放声音，请关闭声音后重试':'未收到现场声音，请检查设备音频设置'),12000);
  stats=setInterval(()=>{void pc.getStats().then(report=>{
   if(disposed)return;
   report.forEach(row=>{if(row.type!=='inbound-rtp'||row.kind!=='audio')return;
    const packets=Number(row.packetsReceived??0);element.dataset.audioPackets=String(packets);element.dataset.audioEnergy=String(row.totalAudioEnergy??0);
    if(packets>lastPackets){lastPackets=packets;emptySince=Date.now();if(!received&&element.readyState>=2&&!element.paused){received=true;clearTimeout(deadline);element.dataset.audioState='playing';onStatus?.('正在播放现场声音')}}
    else if(received&&Date.now()-emptySince>8000)fail('现场声音已中断，请关闭声音后重试');
   });
  }).catch(()=>{})},1000);
  void(async()=>{try{
   await pc.setLocalDescription(await pc.createOffer());
   await new Promise<void>(resolve=>{const finish=()=>{clearTimeout(wait);pc.removeEventListener('icegatheringstatechange',changed);resolve()};const changed=()=>{if(pc.iceGatheringState==='complete')finish()};const wait=setTimeout(finish,850);pc.addEventListener('icegatheringstatechange',changed);changed()});
   if(disposed)return;
   const response=await fetch(endpoint+'?audio=1',{method:'POST',headers:{...headers,'Content-Type':'application/sdp'},body:pc.localDescription!.sdp,signal:AbortSignal.any([abort.signal,AbortSignal.timeout(10000)])});
   resourceURL=response.headers.get('Location')??'';if(disposed){close();return}
   if(!response.ok)throw new Error('现场声音协商失败，请关闭声音后重试');
   const sdp=await response.text();if(disposed)return;
   await pc.setRemoteDescription({type:'answer',sdp});
  }catch(error){if(!disposed)fail((error as Error).message)}})();
  const pagehide=()=>{disposed=true;close()};window.addEventListener('pagehide',pagehide);
  return()=>{disposed=true;window.removeEventListener('pagehide',pagehide);close();element.dataset.audioState='off';onStatus?.('')};
 },[endpoint,enabled,onStatus]);
 return <audio ref={audio} autoPlay aria-label="现场声音" data-audio-state="off"/>;
}
