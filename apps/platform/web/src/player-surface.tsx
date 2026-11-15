import {useEffect,useRef,useState,type ReactNode,type RefObject} from 'react';

type Props={playing:boolean;expanded:boolean;screen:RefObject<HTMLDivElement|null>;children:ReactNode;onToggle:()=>void;onFullscreen:()=>void;className?:string;label?:string};
export function useMediaFullscreen(screen:RefObject<HTMLDivElement|null>){
 const [native,setNative]=useState(false),[expanded,setExpanded]=useState(false);
 useEffect(()=>{const update=()=>setNative(!!screen.current&&document.fullscreenElement===screen.current);document.addEventListener('fullscreenchange',update);return()=>document.removeEventListener('fullscreenchange',update)},[screen]);
 useEffect(()=>{if(!expanded)return;const previous=document.body.style.overflow;document.body.style.overflow='hidden';const escape=(event:KeyboardEvent)=>{if(event.key==='Escape')setExpanded(false)};document.addEventListener('keydown',escape);return()=>{document.body.style.overflow=previous;document.removeEventListener('keydown',escape)}},[expanded]);
 async function toggle(){if(expanded){setExpanded(false);return}try{if(document.fullscreenElement===screen.current)await document.exitFullscreen();else if(screen.current)await screen.current.requestFullscreen();}catch{setExpanded(true)}}
 return {expanded,active:native||expanded,toggle};
}
export function StopGlyph({size=18}:{size?:number}){
 return <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M5 5h14v14H5z"/></svg>;
}

/** Activity belongs to the media surface, independently of its transport. */
export function PlayerSurface({playing,expanded,screen,children,onToggle,onFullscreen,className='',label='视频播放器'}:Props){
 const [visible,setVisible]=useState(true);
 const timer=useRef<ReturnType<typeof setTimeout>>(undefined);
 const focused=useRef(false),hovering=useRef(false);
 const clear=()=>clearTimeout(timer.current);
 function reveal(){
  clear();setVisible(true);
  if(playing&&!focused.current&&!hovering.current)timer.current=setTimeout(()=>setVisible(false),2500);
 }
 useEffect(()=>{reveal();return clear},[playing]);
 return <div ref={screen} className={'vh-screen '+className+(expanded?' is-expanded':'')} role="group" aria-label={label} tabIndex={0} data-controls-visible={!playing||visible}
  onPointerMove={event=>{if(event.pointerType==='mouse'){hovering.current=!!(event.target as Element).closest('.player-toolbar');reveal()}}}
  onPointerEnter={event=>{if(event.pointerType==='mouse')reveal()}}
  onPointerLeave={event=>{hovering.current=false;if(event.pointerType==='mouse'&&playing&&!focused.current){clear();setVisible(false)}}}
  onPointerDown={event=>{if((event.target as Element).closest('button,input,.player-toolbar'))reveal()}}
  onPointerUp={event=>{if(event.pointerType!=='mouse'&&!(event.target as Element).closest('button,input,.player-toolbar')){if(playing&&visible){clear();setVisible(false)}else reveal()}}}
  onFocusCapture={event=>{focused.current=(event.target as Element).matches(':focus-visible')&&!!(event.target as Element).closest('.player-toolbar');reveal()}}
  onBlurCapture={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node|null)){focused.current=false;reveal()}else focused.current=!!(event.relatedTarget as Element|null)?.matches(':focus-visible')&&!!(event.relatedTarget as Element|null)?.closest('.player-toolbar')}}
  onKeyDown={event=>{
   if((event.target as Element).closest('input,button,select,textarea'))return;
   if(event.key===' '||event.key.toLowerCase()==='k'){event.preventDefault();event.stopPropagation();onToggle()}
   if(event.key.toLowerCase()==='f'){event.preventDefault();event.stopPropagation();onFullscreen()}
   reveal();
  }}>
  {children}
 </div>;
}
