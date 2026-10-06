import {useEffect,useRef,useState} from 'react';
import {request,type Envelope} from './data';
export function useResource<T>(path:string|null,demo:T|undefined,refresh=0){
 const key=JSON.stringify([path,demo,refresh]);const activeKey=useRef(key);
 const [state,setState]=useState<{value?:T;error?:string;asOf?:string;loading:boolean;stale:boolean}>({loading:!!path,stale:false});const current=useRef(0);
 useEffect(()=>{activeKey.current=key;const generation=++current.current;if(demo!==undefined){setState({value:demo,asOf:'2026-10-05T14:28:16Z',loading:false,stale:false});return}if(!path){setState({loading:false,stale:false});return}
 setState({loading:true,stale:false});let controller:AbortController|null=null;let inflight=false;let revision=-1;
 const load=async()=>{if(inflight||document.hidden)return;inflight=true;controller=new AbortController();try{const result:Envelope<T>=await request(path,controller.signal);if(current.current===generation&&result.meta.revision>=revision){revision=result.meta.revision;setState({value:result.data,asOf:result.meta.as_of,loading:false,stale:result.meta.data_state!=='fresh'})}}catch(error){if(current.current===generation&&!controller.signal.aborted)setState(old=>({...old,error:error instanceof Error?error.message:'请求失败',loading:false,stale:old.value!==undefined}))}finally{inflight=false}};
 void load();const interval=setInterval(load,5000);const visible=()=>{if(!document.hidden)void load()};document.addEventListener('visibilitychange',visible);
 return()=>{current.current++;controller?.abort();clearInterval(interval);document.removeEventListener('visibilitychange',visible)};
 },[path,JSON.stringify(demo),refresh]);return activeKey.current===key?state:{loading:!!path,stale:false};
}
export function usePagedResource<T extends {id?:string;event_id?:string;sequence?:number}>(path:string|null,demo:T[]|undefined,refresh=0,sequence=false){
 const key=JSON.stringify([path,demo,refresh]);const generation=useRef(0);const activeKey=useRef(key);const cursor=useRef<string|number|null>(null);const loadedPages=useRef(1);const stateRef=useRef<T[]>([]);const controllers=useRef(new Set<AbortController>());const [state,setState]=useState<{value?:T[];error?:string;asOf?:string;loading:boolean;stale:boolean;hasMore:boolean}>({loading:!!path,stale:false,hasMore:false});
 const identity=(item:T)=>item.id||item.event_id||String(item.sequence);
 async function load(more:boolean,epoch:number){if(!path)return;const controller=new AbortController();controllers.current.add(controller);setState(old=>({...old,loading:true}));try{let nextCursor=more?cursor.current:null;let rows:T[]=more?[...stateRef.current]:[];let asOf:string|undefined;let stale=false;let fetched=0;
 const budget=more?1:loadedPages.current;
 for(let page=0;page<budget;page++){const suffix=nextCursor!==null?(path.includes('?')?'&':'?')+(sequence?'after_sequence=':'cursor=')+encodeURIComponent(String(nextCursor)):'';const result=await request<T[]>(path+suffix,controller.signal);if(epoch!==generation.current)return;const paging=result.meta as typeof result.meta&{has_more?:boolean;next_after_sequence?:number};rows.push(...result.data);asOf=paging.as_of;stale ||= paging.data_state!=='fresh';fetched++;
 nextCursor=sequence?(paging.has_more?paging.next_after_sequence??result.data.at(-1)?.sequence??null:null):paging.next_cursor;if(nextCursor===null||nextCursor===undefined){nextCursor=null;break}}
 const map=new Map(rows.map(x=>[identity(x),x]));const next=[...map.values()];if(sequence)next.sort((a,b)=>(a.sequence||0)-(b.sequence||0));stateRef.current=next;cursor.current=nextCursor;loadedPages.current=more?loadedPages.current+fetched:fetched;
 setState({value:next,asOf,loading:false,stale,hasMore:nextCursor!==null});
 }catch(error){if(epoch===generation.current&&!controller.signal.aborted)setState(old=>({...old,error:(error as Error).message,loading:false,stale:old.value!==undefined}))}finally{controllers.current.delete(controller)}}
 useEffect(()=>{const epoch=++generation.current;activeKey.current=key;cursor.current=null;loadedPages.current=1;stateRef.current=[];if(demo!==undefined){stateRef.current=demo;setState({value:demo,asOf:'2026-10-05T14:28:16Z',loading:false,stale:false,hasMore:false});return}setState({loading:!!path,stale:false,hasMore:false});if(!path)return;void load(false,epoch);const tick=setInterval(()=>{if(!document.hidden&&controllers.current.size===0)void load(false,epoch)},5000);return()=>{generation.current++;clearInterval(tick);for(const controller of controllers.current)controller.abort();controllers.current.clear()}},[key]);
 const visible=activeKey.current===key?state:{loading:!!path,stale:false,hasMore:false};return {...visible,loadMore:()=>{if(!state.loading&&cursor.current!==null)void load(true,generation.current)}};
}
