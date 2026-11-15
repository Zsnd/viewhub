import {test} from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackSessionController,PlaybackFailure,presentedFrames} from '../src/playback-session.ts';
const session=id=>({id,rtc:id+'/rtc',webcodecs:id+'/wc',hls:id+'/hls'});
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function harness(create,live=true){
 const updates=[],released=[],pending=[],statuses=[],ended=[];
 const driver=new PlaybackSessionController({create,live,release:async s=>released.push(s.id),update:s=>updates.push(s?.id),status:m=>statuses.push(m),end:m=>ended.push(m),schedule:(fn,ms)=>{const task={fn,ms,cancelled:false};pending.push(task);return()=>task.cancelled=true}});
 return {driver,updates,released,pending,statuses,ended};
}
test('long outage retries without user input and replaces stale media session',async()=>{
 let requests=0;const h=harness(async()=>{requests++;if(requests===2||requests===3)throw Error('offline');return session(String(requests))});
 h.driver.start();await tick();h.driver.failed('no frames');h.driver.failed('duplicate callback');
 assert.deepEqual(h.released,['1']);assert.equal(h.pending.length,1);assert.equal(h.updates.at(-1),undefined);
 for(let i=0;i<3;i++){h.pending[i].fn();await tick()}
 assert.deepEqual(h.updates.filter(Boolean),['1','4']);assert.deepEqual(h.pending.map(t=>t.ms),[1000,2000,4000]);assert.deepEqual(h.ended,[]);
 h.driver.stop();assert.deepEqual(h.released,['1','4']);
});
test('stop cancels a retry and a late play response is released',async()=>{
 let resolve;const h=harness(()=>new Promise(r=>resolve=r));h.driver.start();h.driver.stop();resolve(session('late'));await tick();assert.deepEqual(h.released,['late']);assert.deepEqual(h.updates,[]);
 const r=harness(async()=>{throw Error('offline')});r.driver.start();await tick();r.driver.stop();assert.equal(r.pending[0].cancelled,true);r.pending[0].fn();await tick();assert.equal(r.pending.length,1);
});
test('permission failures and recordings do not auto-replay',async()=>{
 for(const status of [400,401,403,404]){const h=harness(async()=>{throw new PlaybackFailure('denied',status)});h.driver.start();await tick();assert.deepEqual(h.ended,['denied']);assert.equal(h.pending.length,0)}
 const h=harness(async()=>session('recording'),false);h.driver.start();await tick();h.driver.failed('recording ended');assert.equal(h.pending.length,0);assert.deepEqual(h.ended,['recording ended']);
});
test('decoded but discarded frames are not playback progress',()=>{
 assert.equal(presentedFrames({totalVideoFrames:240,droppedVideoFrames:239}),1);
 assert.equal(presentedFrames({totalVideoFrames:600,droppedVideoFrames:599}),1);
 assert.equal(presentedFrames({totalVideoFrames:100,droppedVideoFrames:3}),97);
});
