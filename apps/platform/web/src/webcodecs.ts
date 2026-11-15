export function playWebCodecs(path:string,video:HTMLVideoElement,onFailure:()=>void){
  const canvas=document.createElement('canvas');
  const drawing=canvas.getContext('2d',{alpha:false});
  if(!drawing||!canvas.captureStream)throw new Error('Canvas capture unavailable');

  const url=new URL(path,location.href);
  url.protocol=url.protocol==='https:'?'wss:':'ws:';
  const socket=new WebSocket(url);
  socket.binaryType='arraybuffer';
  let decoder:VideoDecoder|undefined;
  let media:MediaStream|undefined;
  let active=true;
  let waitingForKey=true;

  socket.onmessage=({data}:MessageEvent<string|ArrayBuffer>)=>{
    if(!active)return;
    try{
      if(typeof data==='string'){
        if(decoder)throw new Error('Unexpected decoder configuration');
        const config:VideoDecoderConfig={...JSON.parse(data),optimizeForLatency:true};
        canvas.width=config.codedWidth!;
        canvas.height=config.codedHeight!;
        decoder=new VideoDecoder({
          output(frame){
            try{
              if(!active)return;
              if(!media){
                media=canvas.captureStream();
                video.srcObject=media;
                void video.play().catch(()=>{if(active)onFailure()});
              }
              drawing.drawImage(frame,0,0,canvas.width,canvas.height);
            }catch{if(active)onFailure()}
            finally{frame.close()}
          },
          error(){if(active)onFailure()},
        });
        decoder.configure(config);
        return;
      }
      if(!decoder||data.byteLength<=9)throw new Error('Unexpected video frame');
      const header=new DataView(data);
      const key=header.getUint8(0)===1;
      if(waitingForKey&&!key)return;
      if(decoder.decodeQueueSize>8)throw new Error('Video decoder cannot keep up');
      decoder.decode(new EncodedVideoChunk({
        type:key?'key':'delta',
        timestamp:Number(header.getBigInt64(1)),
        data:new Uint8Array(data,9),
      }));
      waitingForKey=false;
    }catch{if(active)onFailure()}
  };
  socket.onerror=socket.onclose=()=>{if(active)onFailure()};
  return ()=>{
    active=false;
    socket.close();
    if(decoder&&decoder.state!=='closed')decoder.close();
    media?.getTracks().forEach(track=>track.stop());
  };
}
