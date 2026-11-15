import {useState,type CSSProperties} from 'react';
import {SlidersHorizontal,ChevronDown,RotateCcw} from 'lucide-react';
import {Button} from '@/components/ui/button';
type Values={brightness:number;contrast:number;saturation:number};
const original:Values={brightness:100,contrast:100,saturation:100};
export function useImageAdjustments(){
 const [values,setValues]=useState<Values>(original),[compare,setCompare]=useState(false);
 const changed=Object.entries(values).some(([key,value])=>value!==original[key as keyof Values]);
 const style={'--video-filter':compare||!changed?'none':`brightness(${values.brightness}%) contrast(${values.contrast}%) saturate(${values.saturation}%)`} as CSSProperties;
 const controls=<details className="image-adjustments"><summary><SlidersHorizontal size={15}/><span>图像调整</span><ChevronDown size={14}/></summary><p>仅调整当前显示，原始视频不变。</p>
 <div className="image-presets">{[{name:'原始',values:original},{name:'提亮',values:{brightness:115,contrast:95,saturation:100}},{name:'通透',values:{brightness:103,contrast:112,saturation:108}}].map(preset=><Button key={preset.name} size="sm" variant="outline" aria-pressed={JSON.stringify(values)===JSON.stringify(preset.values)} onClick={()=>{setValues(preset.values);setCompare(false)}}>{preset.name}</Button>)}</div>
 {([{key:'brightness',name:'亮度',min:60,max:150},{key:'contrast',name:'对比度',min:60,max:150},{key:'saturation',name:'饱和度',min:0,max:150}] as const).map(item=><label key={item.key} className="image-range"><span>{item.name}<output>{values[item.key]}%</output></span><input type="range" min={item.min} max={item.max} value={values[item.key]} aria-label={item.name} onChange={event=>{setCompare(false);setValues(previous=>({...previous,[item.key]:Number(event.target.value)}))}}/></label>)}
 <div className="image-actions"><Button variant="outline" size="sm" aria-pressed={compare} disabled={!changed} onClick={()=>setCompare(!compare)}>{compare?'返回调整画面':'对比原图'}</Button><Button variant="ghost" size="sm" disabled={!changed} onClick={()=>{setValues(original);setCompare(false)}}><RotateCcw size={13}/>还原</Button></div></details>;
 return {style,controls};
}
