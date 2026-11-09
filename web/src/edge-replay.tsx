import {useEffect,useRef,type RefObject} from 'react';
import {api,headers} from './accounts';

type Transport='rtc'|'mp4';
type Props={
  base:string;slot:string;revision:number;at:number;end:number;video:RefObject<HTMLVideoElement|null>;
  onPosition?:(seconds:number)=>void;onPlaying:(method:'WebRTC 直连'|'服务器中继')=>void;
  onStatus:(message:string)=>void;onEnd:(message:string)=>void;
};

function waitFor(peer:RTCPeerConnection,event:'icegatheringstatechange'|'connectionstatechange',ready:()=>boolean,signal:AbortSignal){
  return new Promise<void>((resolve,reject)=>{
    function cleanup(){peer.removeEventListener(event,check);signal.removeEventListener('abort',abort)}
    function abort(){cleanup();reject(signal.reason)}
    function check(){
      if(ready()){cleanup();resolve()}
      else if(['failed','closed'].includes(peer.connectionState)){cleanup();reject(new Error('直连不可用'))}
    }
    if(signal.aborted){abort();return}
    peer.addEventListener(event,check);
    signal.addEventListener('abort',abort,{once:true});
    check();
  });
}

async function confirmDirect(peer:RTCPeerConnection){
  const reports=await peer.getStats();
  for(const report of reports.values()){
    if(report.type!=='transport'||!report.selectedCandidatePairId)continue;
    const pair=reports.get(report.selectedCandidatePairId);
    const local=reports.get(pair?.localCandidateId);
    const remote=reports.get(pair?.remoteCandidateId);
    if(local&&remote&&local.candidateType!=='relay'&&remote.candidateType!=='relay')return;
  }
  throw new Error('未建立媒体直连');
}

export function EdgeReplay(props:Props){
 const latest=useRef(props);
 latest.current=props;
 const {base,slot,revision,at,end,video}=props;
 useEffect(()=>{
  const element=video.current;
  if(!element)return;
  const el=element;
  const lifetime=new AbortController();
  let disposed=false,stopping=false,mode:Transport|undefined,peer:RTCPeerConnection|undefined,started=false;
  let frameID=0,frameCount=0,reportedFrames=0,sequence=0,mediaStart:number|undefined;
  let currentAt=at,absolute=at,reportedPosition=at,frameTime=0,transition=0;
  let startup:ReturnType<typeof setTimeout>|undefined;
 let progressTimer:ReturnType<typeof setTimeout>|undefined;
 let sentAt=-Infinity;
  let renewal=false;
  const control=(action:string,fields:object={},signal:AbortSignal=AbortSignal.any([lifetime.signal,AbortSignal.timeout(5000)]))=>
   api<void>(base+'/replay-control',{method:'POST',signal,body:JSON.stringify({action,slot,revision,...fields})});
  function release(){
   if(stopping)return;
   stopping=true;
   void fetch(base+'/replay-control',{method:'POST',headers,keepalive:true,body:JSON.stringify({action:'stop',slot,revision})}).catch(()=>{});
  }
  function detach(){
   transition++;
   clearTimeout(startup);
   if(frameID&&typeof el.cancelVideoFrameCallback==='function')el.cancelVideoFrameCallback(frameID);
   frameID=0;
   if(peer){peer.onconnectionstatechange=null;peer.ontrack=null;peer.close();peer=undefined}
   el.pause();el.srcObject=null;el.removeAttribute('src');el.load();
   mediaStart=undefined;
   frameTime=0;
  }
  function fail(message:string){if(!disposed&&!stopping){release();latest.current.onEnd(message)}}
  function observeFrames(){
   const active=transition;
   const frame=(mediaTime:number)=>{
    if(disposed||active!==transition||!mode||el.paused||el.seeking)return;
    if(mediaStart===undefined){
     mediaStart=mediaTime;
     clearTimeout(startup);
     latest.current.onPlaying(mode==='rtc'?'WebRTC 直连':'服务器中继');
    }
    const next=currentAt+Math.max(0,mediaTime-mediaStart);
    if(next<absolute&&absolute-next>1)return;
    absolute=Math.max(absolute,next);
    frameCount++;
    frameTime=performance.now();
    latest.current.onPosition?.(absolute-at);
    if(mode==='mp4'&&started&&frameCount>reportedFrames&&absolute>reportedPosition&&performance.now()-sentAt>=3000){
     pulse();
    }
   };
   if(typeof el.requestVideoFrameCallback==='function'){
    const callback=(_:number,metadata:VideoFrameCallbackMetadata)=>{
     if(disposed||active!==transition)return;
     frame(metadata.mediaTime);
     frameID=el.requestVideoFrameCallback(callback);
    };
    frameID=el.requestVideoFrameCallback(callback);
   }
   return frame;
  }
  let observe:((time:number)=>void)|undefined;
  let lastQuality=0;
  function watch(){
   lastQuality=0;
   observe=observeFrames();
  }
  function onTime(){
   if(typeof el.requestVideoFrameCallback==='function')return;
   const quality=el.getVideoPlaybackQuality();
   const frames=quality.totalVideoFrames-quality.droppedVideoFrames;
   if(frames>lastQuality){lastQuality=frames;observe?.(el.currentTime)}
  }
  async function useMP4(){
   if(disposed||stopping||mode==='mp4')return;
   mode='mp4';
   detach();
   latest.current.onStatus('正在连接中继回放…');
   currentAt=Math.floor(absolute);
   reportedPosition=absolute;
   try{
    await control(started?'fallback':'start',{at:currentAt,transport:'mp4'});
    if(disposed||stopping)return;
    watch();
    el.src=base+'/replay?'+new URLSearchParams({slot,revision:String(revision)});
    void el.play().catch(()=>{});
    startup=setTimeout(()=>fail('中继回放未收到画面'),8500);
   }catch(error){fail((error as Error).message)}
  }
  async function useRTC(){
   const connect=AbortSignal.any([lifetime.signal,AbortSignal.timeout(5000)]);
   const rtc=new RTCPeerConnection({iceServers:[{urls:'stun:stun.cloudflare.com:3478'}]});
   peer=rtc;
   try{
    rtc.addTransceiver('video',{direction:'recvonly'});
    rtc.ontrack=({track})=>{
     if(disposed||peer!==rtc)return;
     el.srcObject=new MediaStream([track]);
     void el.play().catch(()=>{});
    };
    await rtc.setLocalDescription(await rtc.createOffer());
    try{await waitFor(rtc,'icegatheringstatechange',()=>rtc.iceGatheringState==='complete',AbortSignal.any([connect,AbortSignal.timeout(1100)]))}catch{if(connect.aborted)throw connect.reason}
    const answer=await api<{sdp:string}>(base+'/replay-offer',{method:'POST',signal:connect,body:JSON.stringify({slot,revision,sdp:rtc.localDescription?.sdp})});
    await rtc.setRemoteDescription({type:'answer',sdp:answer.sdp});
    await waitFor(rtc,'connectionstatechange',()=>rtc.connectionState==='connected',connect);
    await confirmDirect(rtc);
    if(disposed||stopping||connect.aborted)return;
   }catch{if(!disposed)void useMP4();return}
   mode='rtc';
   try{
    await control('start',{at,transport:'rtc'});
   }catch(error){fail((error as Error).message);return}
   if(disposed||stopping)return;
   started=true;
   watch();
   startup=setTimeout(()=>void useMP4(),4000);
   rtc.onconnectionstatechange=()=>{if(['failed','disconnected'].includes(rtc.connectionState))void useMP4()};
  }
  const onError=()=>{if(mode==='rtc'&&started)void useMP4();else if(mode==='mp4'&&el.getAttribute('src'))fail('中继回放流不可用')};
  const onEnded=()=>fail('录像播放完毕');
  const onPageHide=()=>{release();lifetime.abort();detach();latest.current.onEnd("已停止")};
  function pulse(){
   clearTimeout(progressTimer);
   progressTimer=setTimeout(pulse,3000);
   if(disposed||!mode||stopping)return;
   if(mode==='rtc'&&frameTime&&performance.now()-frameTime>3500){
    if(absolute>=end-1)fail('录像播放完毕');else void useMP4();
    return;
   }
   if(renewal||performance.now()-sentAt<2990||el.paused||el.seeking||frameCount<=reportedFrames||absolute<=reportedPosition||performance.now()-frameTime>3000)return;
   reportedFrames=frameCount;
   reportedPosition=absolute;
   renewal=true;
   sentAt=performance.now();
   const active=transition;
   void control('renew',{sequence:++sequence,frames:frameCount,position:Math.floor(absolute*1000),transport:mode},AbortSignal.any([lifetime.signal,AbortSignal.timeout(2500)]))
    .catch(error=>{if(!disposed&&active===transition&&/会话已到期/.test(error.message))fail('回放会话已到期，请重新播放')})
    .finally(()=>{renewal=false});
  }
  progressTimer=setTimeout(pulse,3000);
  el.addEventListener('timeupdate',onTime);
  el.addEventListener('error',onError);
  el.addEventListener('ended',onEnded);
  window.addEventListener('pagehide',onPageHide);
  latest.current.onStatus('正在协商录像直连…');
  if(typeof RTCPeerConnection==='undefined')void useMP4();else void useRTC();
  return ()=>{
   disposed=true;
   lifetime.abort();
   release();
   clearTimeout(progressTimer);
   el.removeEventListener('timeupdate',onTime);
   el.removeEventListener('error',onError);
   el.removeEventListener('ended',onEnded);
   window.removeEventListener('pagehide',onPageHide);
   detach();
  };
 },[at,base,end,revision,slot,video]);
 return <video ref={video} muted playsInline autoPlay aria-label="历史录像"/>;
}

