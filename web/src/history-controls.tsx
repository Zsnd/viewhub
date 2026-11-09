import type {ReactNode} from 'react';
import {Timeline,clock,type Day} from './timeline';

type Props={
  day?:Day; date:string; zone:string; busy:boolean; position?:number; time:string; error:string;
  active:boolean; paused:boolean;
  onDate:(date:string)=>void; onTime:(time:string)=>void; onJump:()=>void; onSeek:(at:number)=>void;
  onToggle:()=>void; onStop:()=>void; onNext?:()=>void; status?:string; fullscreen?:ReactNode;
};

const sources={onvif:'ONVIF Profile G',isapi:'海康 ISAPI',frigate:'Frigate',gb:'GB28181'};

export function HistoryControls({day,date,zone,busy,position,time,error,active,paused,onDate,onTime,onJump,onSeek,onToggle,onStop,onNext,status,fullscreen}:Props){
  const ready=!!day?.segments.length;
  const playing=active&&!paused;
  const caption=busy?'正在查找录像…':day&&!ready?'所选日期无录像':error?'可切换日期或重新选择时刻。':'点击或拖动时间轴，跳到所选时刻。';
  return <section className="history" aria-label="历史录像">
    <header className="section-head history-head">
      <div>
        <div className="history-title"><h2>录像时间轴</h2>{day?.source&&<small className="history-source">{sources[day.source]}</small>}</div>
        <p className="muted">{caption}</p>
      </div>
      <label className="history-date">日期<input type="date" aria-label="录像日期" value={date} onChange={e=>onDate(e.target.value)}/></label>
    </header>
    {ready&&day?<Timeline key={day.start} day={day} zone={zone} position={position} onSeek={onSeek}/>:<div className="scrubber-empty" aria-hidden="true"/>}
    <div className="history-transport">
      <div className="history-play" role="group" aria-label="录像播放控制">
        <button type="button" className="transport-toggle" disabled={!ready} aria-label={playing?'暂停录像':'播放录像'} onClick={onToggle}>
          <svg width="17" height="17" viewBox="0 0 16 16" aria-hidden="true" fill="currentColor">
            {playing?<path d="M4 3h3v10H4zm5 0h3v10H9z"/>:<path d="m5 2 8 6-8 6z"/>}
          </svg>
        </button>
        <button type="button" disabled={!active} onClick={onStop}>停止回放</button>
        {onNext&&<button type="button" onClick={onNext}>跳到下一段</button>}
        <div className="history-clock"><span>{position===undefined?'--:--:--':clock(position,zone)}</span><small>{active?'播放位置':'上次位置'}</small></div>
      </div>
      <div className="history-tools">
        <form className="jump" onSubmit={e=>{e.preventDefault();onJump()}}>
          <label className="history-jump-time">指定时刻<input type="time" step="1" value={time} onChange={e=>onTime(e.target.value)} required/></label>
          <button disabled={!ready}>跳转</button>
        </form>
        {fullscreen}
      </div>
    </div>
    {status&&status!=='选择录像时刻'&&<p className="muted" role="status">{status}</p>}
    {error&&<p className="timeline-message" role="status">{error}</p>}
  </section>;
}

