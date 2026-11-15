import {useLayoutEffect,useRef,useState,type ReactNode} from 'react';

// Measure the selected button so labels and responsive widths stay aligned.
export function SlidingSegments({value,label,className,children}:{value:string|number;label:string;className:string;children:ReactNode}){
 const root=useRef<HTMLDivElement>(null);
 const [indicator,setIndicator]=useState<{left:number;top:number;width:number;height:number}>();
 useLayoutEffect(()=>{
  const element=root.current!;
  const measure=()=>{
   const selected=element.querySelector<HTMLButtonElement>('button[aria-pressed="true"]');
   if(!selected)return;
   const next={left:selected.offsetLeft,top:selected.offsetTop,width:selected.offsetWidth,height:selected.offsetHeight};
   setIndicator(previous=>previous&&Object.keys(next).every(key=>previous[key as keyof typeof next]===next[key as keyof typeof next])?previous:next);
  };
  measure();
  const observer=new ResizeObserver(measure);
  observer.observe(element);
  element.querySelectorAll('button').forEach(button=>observer.observe(button));
  return ()=>observer.disconnect();
 },[value]);
 return <div ref={root} className={className+' segmented-control'} role="group" aria-label={label} data-ready={!!indicator}>
  {indicator&&<span className="segmented-indicator" aria-hidden="true" style={{width:indicator.width,height:indicator.height,transform:`translate(${indicator.left}px,${indicator.top}px)`}}/>}
  {children}
 </div>;
}
