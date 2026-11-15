import {Button} from '@/components/ui/button';
import {Input} from '@/components/ui/input';
import {Timeline,type Day} from './timeline';

type Props={
  day?:Day; date:string; zone:string; busy:boolean; position?:number; time:string; error:string;
  onDate:(date:string)=>void; onTime:(time:string)=>void; onJump:()=>void; onSeek:(at:number)=>void;
  status?:string;
};

export function HistoryControls({day,date,zone,busy,position,time,error,onDate,onTime,onJump,onSeek,status}:Props){
  const ready=!!day?.segments.length;
  const caption=busy?'正在查找录像…':day&&!ready?'所选日期无录像':error?'可切换日期或重新选择时刻。':'点击或拖动时间轴，跳到所选时刻。';
  return <section className="history" aria-label="历史录像">
    <header className="section-head history-head">
      <div>
        <h2>录像时间轴</h2>
        <p className="muted">{caption}</p>
      </div>
      <label className="history-date">日期<Input type="date" aria-label="录像日期" value={date} onChange={e=>onDate(e.target.value)}/></label>
    </header>
    {ready&&day?<Timeline key={day.start} day={day} zone={zone} position={position} onSeek={onSeek}/>:<div className="scrubber-empty" aria-hidden="true"/>}
    <div className="history-transport">
      <div className="history-tools">
        <form className="jump" onSubmit={e=>{e.preventDefault();onJump()}}>
          <label className="history-jump-time">指定时刻<Input type="time" step="1" value={time} onChange={e=>onTime(e.target.value)} required/></label>
          <Button variant="outline" disabled={!ready}>跳转</Button>
        </form>
      </div>
    </div>
    {status&&status!=='选择录像时刻'&&<p className="muted" role="status">{status}</p>}
    {error&&<p className="timeline-message" role="status">{error}</p>}
  </section>;
}
