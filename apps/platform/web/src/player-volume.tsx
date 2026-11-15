import {useRef} from 'react';
import {Volume2,VolumeX} from 'lucide-react';
export function PlayerVolume({active,disabled,volume,onToggle,onVolume}:{active:boolean;disabled:boolean;volume:number;onToggle:()=>void;onVolume:(value:number)=>void}){
 const previous=useRef(1);
 const audible=active&&volume>0;
 function toggle(){if(!audible&&volume===0)onVolume(previous.current);onToggle()}
 return <div className="player-volume">
  <button className="media-control" type="button" disabled={disabled} aria-label={audible?'关闭声音':'开启声音'} title={disabled?'播放画面后可开启现场声音':audible?'关闭声音':'开启声音'} aria-pressed={audible} onClick={toggle}>{audible?<Volume2 size={19}/>:<VolumeX size={19}/>}</button>
  <div className="player-volume-panel"><input type="range" min="0" max="100" step="1" aria-label="现场声音音量" aria-valuetext={Math.round(volume*100)+'%'} disabled={disabled} value={Math.round(volume*100)} onChange={event=>{const next=Number(event.target.value)/100;if(next>0)previous.current=next;onVolume(next)}}/><output>{Math.round(volume*100)}%</output></div>
 </div>;
}
