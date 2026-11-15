import {useEffect,useRef,useState,type RefObject} from 'react';
import Hls from 'hls.js';
import {headers} from './accounts';
import {playWebCodecs} from './webcodecs';
import {AudioMonitor} from './audio-monitor';
import {presentedFrames} from './playback-session';
export type Session={id:string;rtc:string;webcodecs:string;hls:string};
function sessionURL(id:string){return '/api/session/'+id;}
export function GBPlayer({session,realtime=false,muted=true,volume=1,onStatus,onConnection,onEnd,videoRef,onPosition,onPaused,onAudioStatus}:{session:Session;realtime?:boolean;muted?:boolean;volume?:number;onStatus:(status:string)=>void;onConnection?:(method:'WebRTC 网关'|'WebCodecs'|'HLS'|undefined)=>void;onEnd:(message:string)=>void;videoRef?:RefObject<HTMLVideoElement|null>;onPosition?:(seconds:number)=>void;onPaused?:(paused:boolean)=>void;onAudioStatus?:(status:string)=>void}){
  const localVideo=useRef<HTMLVideoElement>(null);
  const video=videoRef??localVideo;
  const [visible,setVisible]=useState(false);
  const [transport,setTransport]=useState('WebRTC');
  useEffect(()=>{if(video.current)video.current.volume=volume},[volume,video]);
  useEffect(()=>{
    const element=video.current!;
    let peer:RTCPeerConnection|undefined;
    let hls:Hls|undefined;
    let stopWebCodecs:()=>void=()=>{};
    let stopped=false;
    let mode:'WebRTC'|'WebCodecs'|'HLS'='WebRTC';
    let timer:ReturnType<typeof setTimeout>;
    let disconnect:ReturnType<typeof setTimeout>;
    const negotiation=new AbortController();
    let hasPlayed=false;
    let lastProgress=performance.now();
    let previousPosition=-1;
    const release=()=>fetch(sessionURL(session.id),{method:'DELETE',headers,keepalive:true}).catch(()=>{});
    const end=(message:string)=>{
      if(stopped)return;
      stopped=true;
      onConnection?.(undefined);
      negotiation.abort();peer?.close();stopWebCodecs();hls?.destroy();
      onEnd(message);
    };
    const pagehide=()=>{void release()};
    const pageshow=(event:PageTransitionEvent)=>{
      if(event.persisted)end('已结束上次观看，请重新播放');
    };
    const playing=()=>{
      if(stopped)return;
      clearTimeout(timer);
      hasPlayed=true;
      lastProgress=performance.now();
      setVisible(true);
      onConnection?.(mode==='WebRTC'?'WebRTC 网关':mode);
      onStatus('正在播放 · '+mode);
    };
    const failed=()=>end('画面加载失败，请重新播放');
    const heartbeat=setInterval(()=>{
      if(stopped)return;
      const position=mode!=='HLS'&&element.getVideoPlaybackQuality
        ? presentedFrames(element.getVideoPlaybackQuality()) : element.currentTime;
      const progressing=hasPlayed&&!element.paused&&position!==previousPosition;
      previousPosition=position;
      if(progressing)lastProgress=performance.now();
      if(hasPlayed&&performance.now()-lastProgress>=12000){
        if(!realtime&&element.paused)return;
        if(realtime)end('画面中断，正在自动重连…');
        else if(mode!=='HLS'&&!element.paused)fallback();
        else end('画面长时间未播放，已结束观看');
        return;
      }
      void fetch(sessionURL(session.id)+'/heartbeat',{
        method:'POST',headers,body:JSON.stringify({progressing}),
        signal:AbortSignal.timeout(3500),
      }).then(response=>{if(response.status===404)end('观看已超时，请重新播放')}).catch(()=>{});
    },4000);
    window.addEventListener('pagehide',pagehide);
    window.addEventListener('pageshow',pageshow);
    const fallback=()=>{
      if(stopped||mode==='HLS')return;
      onConnection?.(undefined);
      const next=mode==='WebRTC'&&session.webcodecs&&window.VideoDecoder?'WebCodecs':'HLS';
      mode=next;setTransport(next);
      hasPlayed=false;
      previousPosition=-1;
      clearTimeout(timer);
      clearTimeout(disconnect);
      negotiation.abort();
      peer?.close();
      peer=undefined;
      stopWebCodecs();
      element.pause();element.removeAttribute('src');
      element.srcObject=null;
      element.load();
      setVisible(false);
      if(next==='WebCodecs'){
        onStatus('正在连接 WebCodecs…');
        timer=setTimeout(fallback,6500);
        try{
          stopWebCodecs=playWebCodecs(session.webcodecs,element,()=>{
            if(mode==='WebCodecs')fallback();
          });
        }catch{fallback()}
        return;
      }
      onStatus('正在切换到 HLS…');
      timer=setTimeout(failed,25000);
      if(Hls.isSupported()){
        hls=new Hls({lowLatencyMode:true});
        hls.on(Hls.Events.ERROR,(_,event)=>{if(event.fatal)failed()});
        hls.on(Hls.Events.MANIFEST_PARSED,()=>{void element.play().catch(()=>{})});
        hls.attachMedia(element);
        hls.loadSource(session.hls);
      }else if(element.canPlayType('application/vnd.apple.mpegurl')){
        element.src=session.hls; void element.play().catch(()=>{});
      }else{failed()}
    };
    element.addEventListener('playing',playing);
    const mediaError=()=>mode==='HLS'?failed():fallback();
    element.addEventListener('error',mediaError);
    onStatus('正在连接 WebRTC…');
    timer=setTimeout(fallback,9000);
    void (async()=>{
      try{
        if(!window.RTCPeerConnection){fallback();return}
        const connection=new RTCPeerConnection();
        peer=connection;
        connection.addTransceiver('video',{direction:'recvonly'});
        connection.addTransceiver('audio',{direction:'recvonly'});
        const remoteStream=new MediaStream();
        connection.ontrack=event=>{
          if(stopped||mode!=='WebRTC')return;
          remoteStream.addTrack(event.track);
          // Audio/video arrive as separate track events. Reassigning srcObject
          // restarts the media clock and compositor even for the same stream.
          if(element.srcObject!==remoteStream)element.srcObject=remoteStream;
          void element.play().catch(()=>{});
        };
        connection.onconnectionstatechange=()=>{
          if(stopped||mode!=='WebRTC')return;
          clearTimeout(disconnect);
          if(connection.connectionState==='failed')fallback();
          if(connection.connectionState==='disconnected')disconnect=setTimeout(fallback,2500);
        };
        await connection.setLocalDescription(await connection.createOffer());
        await new Promise<void>(resolve=>{
          const finish=()=>{clearTimeout(wait);connection.removeEventListener('icegatheringstatechange',changed);resolve()};
          const changed=()=>{if(connection.iceGatheringState==='complete')finish()};
          const wait=setTimeout(finish,850);
          connection.addEventListener('icegatheringstatechange',changed);
          changed();
        });
        if(negotiation.signal.aborted)return;
        const result=await fetch(session.rtc,{
          method:'POST',headers:{...headers,'Content-Type':'application/sdp'},
          signal:negotiation.signal,body:connection.localDescription!.sdp,
        });
        if(!result.ok)throw new Error('WebRTC failed');
        await connection.setRemoteDescription({type:'answer',sdp:await result.text()});
      }catch{if(mode==='WebRTC')fallback()}
    })();
    return ()=>{
      stopped=true;
      window.removeEventListener('pagehide',pagehide);
      window.removeEventListener('pageshow',pageshow);
      clearInterval(heartbeat);clearTimeout(timer);clearTimeout(disconnect);
      negotiation.abort();
      element.removeEventListener('playing',playing);
      element.removeEventListener('error',mediaError);
      peer?.close();stopWebCodecs();hls?.destroy();
      element.srcObject=null;element.removeAttribute('src');element.load();
    };
  },[session,realtime,onStatus,onConnection,onEnd,video]);
  return <><AudioMonitor volume={volume} endpoint={session.rtc} enabled={!muted&&transport!=='WebRTC'} onStatus={onAudioStatus}/><video ref={video} hidden={!visible} onTimeUpdate={e=>onPosition?.(e.currentTarget.currentTime)} onPlaying={()=>onPaused?.(false)} onPause={()=>onPaused?.(true)} autoPlay muted={muted} playsInline aria-label="监控视频"/></>;
}

