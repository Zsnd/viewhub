import {useEffect,useRef,type RefObject} from 'react';
import Hls from 'hls.js';

const recordingStartupTimeout=45000;

function socketURL(path:string){
  const address=new URL(path,location.href);
  address.protocol=location.protocol==='https:'?'wss:':'ws:';
  return address;
}

function connectDirect(path:string,video:HTMLVideoElement,fallback:()=>void){
  const peer=new RTCPeerConnection({iceServers:[
    {urls:['stun:stun.cloudflare.com:3478','stun:stun.l.google.com:19302']},
  ]});
  const socket=new WebSocket(socketURL(path));
  let stopped=false;
  let sentOffer=false;
  let remoteReady=false;
  const localCandidates:string[]=[];
  const remoteCandidates:string[]=[];
  const send=(type:string,value:string)=>socket.send(JSON.stringify({type,value}));
  peer.addTransceiver('video',{direction:'recvonly'});
  peer.onicecandidate=({candidate})=>{
    if(stopped||!candidate?.candidate)return;
    if(sentOffer&&socket.readyState===WebSocket.OPEN)send('webrtc/candidate',candidate.candidate);
    else localCandidates.push(candidate.candidate);
  };
  peer.ontrack=({track})=>{
    if(stopped)return;
    video.srcObject=new MediaStream([track]);
    void video.play().catch(()=>{});
  };
  peer.onconnectionstatechange=()=>{
    if(peer.connectionState==='failed')fallback();
  };
  socket.onopen=async()=>{
    try{
      await peer.setLocalDescription(await peer.createOffer());
      if(stopped)return;
      send('webrtc/offer',peer.localDescription!.sdp);
      sentOffer=true;
      for(const candidate of localCandidates)send('webrtc/candidate',candidate);
      localCandidates.length=0;
    }catch{if(!stopped)fallback()}
  };
  const addCandidate=(candidate:string)=>peer.addIceCandidate({candidate,sdpMLineIndex:0});
  socket.onmessage=async({data})=>{
    try{
      const {type,value}=JSON.parse(data);
      if(type==='webrtc/answer'){
        await peer.setRemoteDescription({type:'answer',sdp:value});
        if(stopped)return;
        remoteReady=true;
        for(const candidate of remoteCandidates)await addCandidate(candidate);
        remoteCandidates.length=0;
      }else if(type==='webrtc/candidate'&&value){
        if(remoteReady)await addCandidate(value);
        else remoteCandidates.push(value);
      }else if(type==='error'){
        fallback();
      }
    }catch{if(!stopped)fallback()}
  };
  socket.onerror=fallback;
  socket.onclose=()=>{if(peer.connectionState!=='connected')fallback()};
  return ()=>{
    stopped=true;
    socket.onclose=null;
    socket.onerror=null;
    socket.close();
    peer.close();
    video.srcObject=null;
  };
}

export function Player({live,rtc,hls,onStatus,onConnection,onEnd,videoRef,onPosition,onPaused}:{
  live?:string;rtc?:string;hls:string;onStatus:(text:string)=>void;onConnection?:(method:'WebRTC 直连'|'服务器中继'|'HLS'|undefined)=>void;onEnd:(text:string)=>void;
  videoRef?:RefObject<HTMLVideoElement|null>;onPosition?:(seconds:number)=>void;onPaused?:(paused:boolean)=>void;
}){
  const localVideo=useRef<HTMLVideoElement>(null);
  const video=videoRef??localVideo;
  useEffect(()=>{
    const element=video.current!;
    let mode:'direct'|'relay'|'recording'=live&&rtc?'direct':live?'relay':'recording';
    let timer:ReturnType<typeof setTimeout>;
    let disposed=false;
    let cleanup=()=>{};
    let lastFrame=0;
    let stalled=0;
    const failed=()=>onEnd('视频画面加载失败，请重试');
    const fallback=()=>{
      if(disposed||mode!=='direct'||!live)return;
      mode='relay';
      onConnection?.(undefined);
      clearTimeout(timer);
      cleanup();
      onStatus('直连不可用，正在连接服务器中继…');
      timer=setTimeout(failed,12000);
      cleanup=startRelay(live);
    };
    const playing=()=>{
      clearTimeout(timer);
      onConnection?.(mode==='direct'?'WebRTC 直连':mode==='relay'?'服务器中继':'HLS');
      onStatus('正在播放 · '+(mode==='direct'?'WebRTC 直连':mode==='relay'?'服务器中继':'历史录像'));
    };
    const ended=()=>onEnd('录像播放完毕');
    const mediaError=()=>mode==='direct'?fallback():failed();
    const monitor=setInterval(()=>{
      if(mode!=='direct'||element.paused||!element.srcObject)return;
      const frames=element.getVideoPlaybackQuality().totalVideoFrames;
      stalled=frames===lastFrame?stalled+1:0;
      lastFrame=frames;
      if(stalled>=3)fallback();
    },2000);
    element.addEventListener('playing',playing);
    element.addEventListener('error',mediaError);
    element.addEventListener('ended',ended);
    const startRelay=(path:string)=>{
      const managed=(window as Window & {ManagedMediaSource?:typeof MediaSource}).ManagedMediaSource;
      const Media=window.MediaSource??managed;
      if(!Media){failed();return ()=>{}}
      const media=new Media();
      const objectURL=URL.createObjectURL(media);
      const address=socketURL(path);
      const queue:ArrayBuffer[]=[];
      let socket:WebSocket|undefined;
      let buffer:SourceBuffer|undefined;
      const flush=()=>{
        if(!buffer||buffer.updating||media.readyState!=='open')return;
        try{
          if(buffer.buffered.length&&element.currentTime-buffer.buffered.start(0)>25){
            buffer.remove(buffer.buffered.start(0),element.currentTime-10);
          }else if(queue.length){
            buffer.appendBuffer(queue.shift()!);
          }
        }catch{failed()}
      };
      const progress=setInterval(()=>{
        if(element.paused||socket?.readyState!==WebSocket.OPEN)return;
        const {totalVideoFrames,droppedVideoFrames}=element.getVideoPlaybackQuality();
        socket.send(JSON.stringify({frames:totalVideoFrames-droppedVideoFrames}));
      },3000);
      media.addEventListener('sourceopen',()=>{
        if(disposed)return;
        socket=new WebSocket(address);
        socket.binaryType='arraybuffer';
        socket.onclose=failed;
        socket.onerror=failed;
        socket.onmessage=({data})=>{
          try{
            if(typeof data==='string'){
              const message=JSON.parse(data);
              if(message.type==='error'){failed();return}
              if(message.type!=='mse'||buffer)return;
              buffer=media.addSourceBuffer(message.value);
              buffer.addEventListener('updateend',flush);
              buffer.addEventListener('error',failed);
              void element.play().catch(()=>{});
              return;
            }
            if(queue.length>=128){failed();return}
            queue.push(data);
            flush();
          }catch{failed()}
        };
      },{once:true});
      element.disableRemotePlayback=true;
      element.src=objectURL;
      return ()=>{
        clearInterval(progress);
        if(socket){socket.onclose=null;socket.onerror=null;socket.onmessage=null;socket.close()}
        URL.revokeObjectURL(objectURL);
      };
    };
    if(live&&rtc&&typeof RTCPeerConnection==='function'){
      onStatus('正在尝试 WebRTC 直连…');
      timer=setTimeout(fallback,6000);
      try{cleanup=connectDirect(rtc,element,fallback)}catch{fallback()}
    }else if(live){
      mode='relay';
      onStatus('正在连接服务器中继…');
      timer=setTimeout(failed,12000);
      cleanup=startRelay(live);
    }else if(Hls.isSupported()){
      onStatus('正在加载录像…');
      timer=setTimeout(failed,recordingStartupTimeout);
      const player=new Hls({lowLatencyMode:false});
      player.on(Hls.Events.ERROR,(_,event)=>{if(event.fatal)failed()});
      player.on(Hls.Events.MANIFEST_PARSED,()=>{void element.play().catch(()=>{})});
      player.attachMedia(element);
      player.loadSource(hls);
      cleanup=()=>player.destroy();
    }else if(element.canPlayType('application/vnd.apple.mpegurl')){
      onStatus('正在加载录像…');
      timer=setTimeout(failed,recordingStartupTimeout);
      element.src=hls;
    }else{failed()}
    return ()=>{
      disposed=true;
      clearTimeout(timer);
      clearInterval(monitor);
      element.removeEventListener('playing',playing);
      element.removeEventListener('error',mediaError);
      element.removeEventListener('ended',ended);
      cleanup();
      element.removeAttribute('src');
      element.load();
    };
  },[live,rtc,hls,onStatus,onConnection,onEnd,video]);
  return <video ref={video} autoPlay muted playsInline aria-label="监控视频"
    onTimeUpdate={event=>onPosition?.(event.currentTarget.currentTime)}
    onPlaying={()=>onPaused?.(false)} onPause={()=>onPaused?.(true)}/>;
}

