export type PlaybackSession={id:string;rtc:string;webcodecs:string;hls:string};
export class PlaybackFailure extends Error {
  status:number;
  constructor(message:string,status:number){super(message);this.status=status;}
}
export function permanentPlaybackFailure(error:unknown){return error instanceof PlaybackFailure&&[400,401,403,404].includes(error.status)}
export function presentedFrames(quality:{totalVideoFrames:number;droppedVideoFrames:number}){
  return Math.max(0,quality.totalVideoFrames-quality.droppedVideoFrames);
}
type Options={
  create:(signal:AbortSignal)=>Promise<PlaybackSession>;
  release:(session:PlaybackSession)=>Promise<unknown>;
  update:(session:PlaybackSession|undefined)=>void;
  status:(message:string)=>void;
  end:(message:string)=>void;
  live:boolean;
  schedule?:(callback:()=>void,delay:number)=>()=>void;
};
// Owns viewer intent. Transport failure releases the old session but does not
// cancel live viewing; stop/unmount is the only way to cancel scheduled retries.
export class PlaybackSessionController {
  private options:Options;
  private stopped=false;
  private current:PlaybackSession|undefined;
  private request:AbortController|undefined;
  private cancelRetry:(()=>void)|undefined;
  private retrying=false;
  private attempts=0;
  constructor(options:Options){this.options=options;}
  start(){void this.acquire()}
  private release(session:PlaybackSession){void this.options.release(session).catch(()=>{})}
  private async acquire(){
    if(this.stopped)return;
    this.retrying=false;
    const request=new AbortController();this.request=request;
    try{
      const session=await this.options.create(request.signal);
      if(this.stopped||request.signal.aborted){this.release(session);return}
      this.current=session;this.options.update(session);
    }catch(error){
      if(this.stopped||request.signal.aborted)return;
      const message=error instanceof Error?error.message:'连接失败';
      if(!this.options.live||permanentPlaybackFailure(error)){this.options.end(message);return}
      this.failed(message);
    }
  }
  played(){this.attempts=0;}
  failed(message:string){
    if(this.stopped||this.retrying)return;
    this.retrying=true;
    this.request?.abort();
    const old=this.current;this.current=undefined;
    this.options.update(undefined);
    if(old)this.release(old);
    if(!this.options.live){this.options.end(message);return}
    const delay=Math.min(10000,1000*2**Math.min(this.attempts++,4));
    this.options.status(`连接中断，${delay/1000} 秒后自动重连…`);
    const schedule=this.options.schedule??((callback,ms)=>{const timer=setTimeout(callback,ms);return ()=>clearTimeout(timer)});
    this.cancelRetry=schedule(()=>{this.cancelRetry=undefined;if(!this.stopped){this.options.status('正在重新连接…');void this.acquire()}},delay);
  }
  stop(){
    if(this.stopped)return;
    this.stopped=true;this.cancelRetry?.();this.request?.abort();
    if(this.current)this.release(this.current);
    this.current=undefined;
  }
}
