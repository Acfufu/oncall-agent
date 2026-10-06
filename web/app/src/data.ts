import type {WorkspaceFixture,ResponseMeta,Incident,DiagnosisRun,GraphSnapshot,RunEvent,EvidenceRecord} from './domain';
const modules=import.meta.glob('./fixtures/*.json',{eager:true,import:'default'});
export const fixtures=Object.values(modules) as WorkspaceFixture[];
export interface Envelope<T>{data:T;meta:ResponseMeta}
export class ApiFailure extends Error {constructor(message:string,public status:number){super(message)}}
let csrf='';
export function setCSRF(value:string){csrf=value}
export async function request<T>(path:string,signal?:AbortSignal,init:RequestInit={}):Promise<Envelope<T>>{
 const headers=new Headers(init.headers);if(init.body)headers.set('Content-Type','application/json');if(init.method&&init.method!=='GET')headers.set('X-CSRF-Token',csrf);
 const response=await fetch('/api/v1'+path,{...init,headers,credentials:'same-origin',signal});
 const body=await response.json().catch(()=>({error:'接口未返回有效 JSON'}));
 if(!response.ok)throw new ApiFailure(body.error||'请求失败',response.status);
 if(!body.meta||!('data' in body))throw new ApiFailure('接口契约不匹配',502);
 return body;
}
export interface Workspace {incident:Incident;run:DiagnosisRun|null;graph:GraphSnapshot|null;events:RunEvent[];evidence:EvidenceRecord[]}
export interface Summary {facets?:{environments:string[];services:string[]};severity_distribution?:Record<string,number>;history?:import('./History').HistoryPoint[];active_incidents:number|null;no_evidence_incidents:number|null;succeeded_runs:number;cited_succeeded_runs:number;mean_diagnosis_ms:number|null;source_unavailable_incidents:number}
export function demoSummary(items:WorkspaceFixture[]):Summary{
 const runs=items.map(x=>x.run);const completed=runs.filter(x=>['succeeded','failed'].includes(x.status)&&x.duration_ms!==null);
 return {active_incidents:items.filter(x=>x.incident.lifecycle_status==='active').length,no_evidence_incidents:runs.filter(x=>x.evidence_status==='no_evidence').length,succeeded_runs:runs.filter(x=>x.status==='succeeded').length,cited_succeeded_runs:runs.filter(x=>x.status==='succeeded'&&x.citation_ids.length>0).length,mean_diagnosis_ms:completed.length?completed.reduce((n,x)=>n+x.duration_ms!,0)/completed.length:null,source_unavailable_incidents:runs.filter(x=>x.evidence_status==='source_unavailable').length};
}
export function stages(events:RunEvent[],runStatus?:string,notificationStatus?:string){return (['receive','queue','retrieval','report','judge','notification'] as const).map(stage=>{const rows=events.filter(x=>x.stage===stage).sort((a,b)=>a.sequence-b.sequence);const last=rows.at(-1);return {stage,rows,last,display:last?.kind==='started'&&(stage==='notification'?['sent','failed','disabled'].includes(notificationStatus||''):['succeeded','failed','cancelled'].includes(runStatus||''))?'terminal_missing':last?.kind||'unknown',retries:Math.max(0,...rows.map(x=>x.stage_attempt))-1}})}
export const labels:Record<string,string>={terminal_missing:'终态未记录 / 未知',receive:'接收告警',queue:'排队',retrieval:'检索',report:'报告组装',judge:'评分',notification:'通知',succeeded:'成功',failed:'失败',queued:'排队中',running:'运行中',cancelled:'已取消',started:'运行中',skipped:'未启用',active:'活跃',resolved:'已恢复',unknown:'未知',has_citations:'含引用',no_evidence:'无证据',source_unavailable:'来源不可用',not_evaluated:'待评估',unverified:'待核验',confirmed:'已核验',rejected:'已否定',not_applicable:'不适用',pending:'待投递',sent:'已送达',disabled:'未启用',contains:'包含',triggered:'触发',retrieved:'检索到',cites:'引用',produced:'组装',suggests:'提示',possibly_supports:'可能支持',depends_on:'依赖',observed_on:'观测于'};
export function label(x:string){return labels[x]||x}
export function duration(x:number|null|undefined){return x==null?'—':x>=1000?(x/1000).toFixed(2)+' s':Math.round(x)+' ms'}
export function time(x:string|null|undefined,zone?:string){return x?new Date(x).toLocaleString('zh-CN',{timeZone:zone,hour12:false}):'—'}
