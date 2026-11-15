import {Button} from '@/components/ui/button';
import {Input} from '@/components/ui/input';
import {useRef,useState} from 'react';

export type Segment={start:number;end:number};
export type Day={start:number;end:number;segments:Segment[]};

export function clock(value:number,zone:string){
  return new Intl.DateTimeFormat('zh-CN',{timeZone:zone,hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23'}).format(value*1000);
}

function contains(segments:Segment[],at:number){
  return segments.some(segment=>segment.start<=at&&at<segment.end);
}

export function Timeline({day,zone,position,onSeek}:{day:Day;zone:string;position?:number;onSeek:(at:number)=>void}){
  const [zoom,setZoom]=useState(0);
  const [windowIndex,setWindowIndex]=useState(0);
  const [preview,setPreview]=useState<number>();
  const pointer=useRef<number|null>(null);
  const keyboardPoint=useRef<number|undefined>(undefined);
  const ticks=zoom===600?5:4;
  const [dragging,setDragging]=useState(false);
  const step=zoom||day.end-day.start;
  const start=day.start+windowIndex*step;
  const end=Math.min(start+step,day.end);
  const width=end-start;
  const windows=Math.ceil((day.end-day.start)/step);
  const span=(at:number)=>100*(at-start)/width;
  const clamp=(at:number)=>Math.max(start,Math.min(end-1,at));
  const point=(clientX:number,rect:DOMRect)=>clamp(start+Math.floor(width*(clientX-rect.left)/rect.width));
  const visible=position!==undefined&&position>=start&&position<end;
  const value=visible?position:start;
  const previewText=preview===undefined?'':clock(preview,zone)+' · '+(contains(day.segments,preview)?'有录像':'无录像');
  const segments=day.segments.filter(s=>s.start<end&&s.end>start);

  function changeZoom(next:number){
    const anchor=visible?position:(zoom?start+width/2:day.segments[0]?.start??start);
    setWindowIndex(next?Math.floor((anchor-day.start)/next):0);
    setZoom(next);
    setPreview(undefined);
    keyboardPoint.current=undefined;
  }

  return <div className="recording-timeline">
    <div className="timeline-toolbar">
      <div className="timeline-legend">
        <span><i className="legend-recorded"/>有录像</span>
        <span><i/>无录像</span>
      </div>
      <div className="timeline-scale" aria-label="时间轴缩放">
        <Button variant="outline" type="button" aria-label="上一时间段" disabled={windowIndex===0} onClick={()=>{setWindowIndex(windowIndex-1);setPreview(undefined);keyboardPoint.current=undefined}}>‹</Button>
        {[[0,'全天'],[3600,'1 小时'],[600,'10 分钟']].map(([size,label])=><Button variant="outline" key={size} type="button" aria-pressed={zoom===size} onClick={()=>changeZoom(Number(size))}>{label}</Button>)}
        <Button variant="outline" type="button" aria-label="下一时间段" disabled={windowIndex>=windows-1} onClick={()=>{setWindowIndex(windowIndex+1);setPreview(undefined);keyboardPoint.current=undefined}}>›</Button>
      </div>
    </div>
    <div className="scrubber" role="slider" aria-label="录像播放位置" aria-valuemin={0} aria-valuemax={width-1}
      aria-valuenow={value-start} aria-valuetext={clock(value,zone)} tabIndex={0} data-position={visible?position:undefined}
      onKeyDown={event=>{
        const from=keyboardPoint.current??value;
        const next={ArrowLeft:from-10,ArrowRight:from+10,ArrowDown:from-10,ArrowUp:from+10,Home:start,End:end-1}[event.key];
        if(next===undefined)return;
        event.preventDefault();
        if(event.repeat)return;
        const at=clamp(next);
        keyboardPoint.current=contains(day.segments,at)?undefined:at;
        onSeek(at);
      }}
      onPointerDown={event=>{
        if(!event.isPrimary||event.button!==0)return;
        keyboardPoint.current=undefined;
        pointer.current=event.pointerId;
        event.currentTarget.setPointerCapture(event.pointerId);
        setDragging(true);
        setPreview(point(event.clientX,event.currentTarget.getBoundingClientRect()));
      }}
      onPointerMove={event=>{
        if(pointer.current===event.pointerId||pointer.current===null&&event.pointerType==='mouse'){
          setPreview(point(event.clientX,event.currentTarget.getBoundingClientRect()));
        }
      }}
      onPointerLeave={()=>{if(pointer.current===null)setPreview(undefined)}}
      onPointerUp={event=>{
        if(pointer.current!==event.pointerId)return;
        pointer.current=null;
        setDragging(false);
        setPreview(undefined);
        onSeek(point(event.clientX,event.currentTarget.getBoundingClientRect()));
      }}
      onLostPointerCapture={()=>{pointer.current=null;setDragging(false);setPreview(undefined)}}
      onPointerCancel={()=>{pointer.current=null;setDragging(false);setPreview(undefined)}}>
      <div className="scrubber-track" aria-hidden="true">
        {segments.map(s=><span key={s.start} className="scrubber-recorded" style={{left:span(Math.max(s.start,start))+'%',width:(span(Math.min(s.end,end))-span(Math.max(s.start,start)))+'%'}}/>)}
      </div>
      {visible&&<span className="scrubber-head" aria-hidden="true" style={{left:span(position)+'%'}}/>}
      {preview!==undefined&&<><span className={'scrubber-preview'+(dragging?' dragging':'')} aria-hidden="true" style={{left:span(preview)+'%'}}/><span className="scrubber-tooltip" aria-hidden="true" style={{left:'clamp(72px, '+span(preview)+'%, calc(100% - 72px))'}}>{previewText}</span></>}
      <div className="scrubber-ruler" aria-hidden="true">
        {Array.from({length:ticks*5+1},(_,i)=><i key={i} className={i%5===0?'major':''} style={{left:100*i/(ticks*5)+'%'}}/>)}
      </div>
    </div>
    <div className="axis" aria-hidden="true">{Array.from({length:ticks+1},(_,t)=><span key={t}>{t===ticks&&end===day.end?'24:00':clock(start+Math.round(width*t/ticks),zone).slice(0,5)}</span>)}</div>
  </div>;
}
