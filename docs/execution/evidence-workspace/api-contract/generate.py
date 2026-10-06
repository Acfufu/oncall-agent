import re,json,pathlib,yaml
root=pathlib.Path('.')
S={}
def ref(n): return {'$ref':'#/components/schemas/'+n}
def arr(s): return {'type':'array','items':s}
def obj(p,required=None): return {'type':'object','properties':p,'required':list(p) if required is None else required}
def typ(t):
 if t.startswith('*'): return {'anyOf':[typ(t[1:]),{'type':'null'}]}
 if t.startswith('[]'): return arr(typ(t[2:]))
 if t.startswith('map['): return {'type':'object','additionalProperties':typ(t.split(']',1)[1])}
 return {'string':{'type':'string'},'bool':{'type':'boolean'},'int':{'type':'integer'},'int64':{'type':'integer'},'any':{}}.get(t,ref(t))
for file,names in [('internal/workspace/model.go',None),('internal/workspace/topology.go',{'TopologyNode','TopologyEdge','PotentialImpact','TopologySnapshot'}),('internal/handler/workspace_metrics.go',{'metricResult','metricSeries'}),('internal/agent/react.go',{'Citation'})]:
 text=pathlib.Path(file).read_text()
 for n,body in re.findall(r'type (\w+) struct \{\n(.*?)\n\}',text,re.S):
  if names is not None and n not in names: continue
  p={};required=[]
  for t,tag,optional in re.findall(r'^\s*\w+\s+(\S+)\s+`json:"([^",]+)(,omitempty)?"`',body,re.M):
   p[tag]= arr({'type':'array','prefixItems':[{'type':'number'},{'type':['number','null']}],'minItems':2,'maxItems':2}) if t=='[][2]any' else typ(t)
   if not optional: required.append(tag)
  if p:S[n]=obj(p,required)
S['Meta']=obj({'request_id':typ('string'),'as_of':{'type':'string','format':'date-time'},'data_state':{'type':'string'},'mode':{'type':'string','enum':['live']},'revision':typ('int'),'next_cursor':typ('*string'),'truncated':typ('bool'),'has_more':typ('bool'),'next_after_sequence':typ('int')},['request_id','as_of','data_state','mode','revision','next_cursor','truncated'])
S['Error']=obj({'error':typ('string'),'code':typ('string'),'retryable':typ('bool'),'request_id':typ('string')})
S['AuthError']=obj({'error':typ('string'),'code':typ('string')},['error'])
S['DocumentInput']=obj({'title':{'type':'string','minLength':1,'description':'Trimmed nonempty; maximum 512 UTF-8 bytes'},'content':{'type':'string','minLength':1,'description':'Trimmed nonempty Markdown; maximum 512 KiB UTF-8 bytes'},'source_kind':{'type':'string','enum':['upload'],'default':'upload'},'environment':typ('string')},['title','content'])
S['DocumentWrite']=obj({'document':ref('Document'),'version':ref('Version')})
S['HistoryBucket']=obj({**{k:typ('int') for k in ['received_incidents','succeeded_runs','no_evidence_runs','cited_succeeded_runs','latency_samples']},'timestamp':typ('string'),'mean_diagnosis_ms':{'type':['number','null']},'citation_coverage':{'type':['number','null']}})
S['Summary']=obj({**{k:typ('int') for k in ['active_incidents','no_evidence_incidents','source_unavailable_incidents','succeeded_runs','cited_succeeded_runs','latency_samples']},'mean_diagnosis_ms':{'type':['number','null']},'citation_coverage':{'type':['number','null']},'comparison':{'type':'null'},'coverage':typ('string'),'history':arr(ref('HistoryBucket')),'history_basis':typ('string')})
S['Summary']['description']='Only recorded scoped SQLite facts. UTC received-day buckets; no synthetic zero gaps or historical coverage claim; empty denominators are null.'
def envelope(s):return obj({'data':s,'meta':ref('Meta')})
def param(n,where='query',schema=None,required=False,description=''):return {'name':n,'in':where,'required':required,'schema':schema or typ('string'),'description':description}
def limit(default,max):return param('limit',schema={'type':'integer','minimum':1,'maximum':max,'default':default})
page=[limit(20,100),param('cursor',description='Opaque base64 JSON continuation bound to path and all filters except limit. Descending timestamp plus ID; invalid or filter-mismatched cursor returns 400.')]
filters=[param(k) for k in ['environment','service','status']]+[param(k,schema={'type':'string','format':'date-time'}) for k in ['from','to']]
paths={}
def route(path,method,summary,data=None,code=200,params=None,body=None,desc='',legacy=False):
 response={'description':'Success','content':{'application/json':{'schema': data or {'type':'object'}}}}
 if not legacy:response['content']['application/json']['schema']=envelope(data or {'type':'object'})
 responses={str(code):response}
 for c in [400,401,403,404,409,413,429,503,504]:responses[str(c)]={'description':{400:'Invalid request',401:'Authentication required',403:'Scope or CSRF rejected',404:'Missing record',409:'Version/idempotency conflict',413:'Body budget exceeded',429:'Rate limit (Retry-After: 60)',503:'Dependency unavailable; no synthetic success',504:'Request timeout'}[c],'content':{'application/json':{'schema':ref('AuthError' if c in [401,403,429] else 'Error')}}}
 p=[param(n,'path',required=True) for n in re.findall(r'{(\w+)}',path)]+(params or [])
 op={'operationId':method+'_'+re.sub(r'[^a-zA-Z0-9]+','_',path).strip('_'),'summary':summary,'description':desc,'responses':responses}
 if p:op['parameters']=p
 if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':body}}}
 paths.setdefault(path,{})[method]=op
route('/api/v1/system/status','get','Probe actual dependency readiness',desc='Database read errors return 503. Dependency fields contain state and last_success_at, without URLs or credentials. Capability configuration does not establish source health.')
route('/api/v1/workspace/summary','get','Recorded scoped counts and history',ref('Summary'),params=filters)
route('/api/v1/incidents','get','Incident list',arr(ref('Incident')),params=filters+page)
route('/api/v1/incidents/{id}','get','Incident and runs',obj({'incident':ref('Incident'),'runs':arr(ref('Run'))}),params=[param('run_id')])
route('/api/v1/incidents/{id}/graph','get','Bounded incident evidence graph',ref('Graph'),params=[param('run_id'),param('focus_id'),param('depth',schema={'type':'integer','minimum':1,'maximum':2,'default':1}),param('node_limit',schema={'type':'integer','minimum':1,'maximum':300,'default':60}),param('edge_limit',schema={'type':'integer','minimum':1,'maximum':600,'default':120})],desc='Run and focus must belong to this incident. No focus/depth returns full bounded graph. Explicit focus/depth selects its neighborhood. meta revision/as_of match the SQL graph snapshot; omitted_counts and truncated expose budget omissions.')
key=param('Idempotency-Key','header',{'type':'string','minLength':1,'maxLength':200},True,'Reuse same key for same payload; changed payload conflicts with 409.')
match=param('If-Match','header',required=True,description='Exact current active version ID, optionally quoted; wildcard forbidden. Missing returns 400, stale returns 409.')
route('/api/v1/incidents/{id}/runs','post','Request re-diagnosis',obj({'run_id':typ('string'),'status':typ('string'),'dispatch_state':typ('string')}),202,[key],desc='Optional JSON body (default {}), maximum 32 KiB; canonical body participates in key conflict checks. Accepted durable run; pending dispatch is not completion. 10 manual requests/minute per identity.')
route('/api/v1/runs/{id}','get','Run fact',ref('Run'),desc='ETag contains quoted run revision.')
route('/api/v1/runs/{id}/events','get','Monotonic run event page',arr(ref('Event')),params=[param('after_sequence',schema={'type':'integer','minimum':0,'maximum':1073741824,'default':0}),limit(100,200)],desc='Ascending sequence strictly greater than after_sequence. meta has_more and next_after_sequence enable polling.')
route('/api/v1/evidence/{id}','get','Immutable evidence fact',ref('Evidence'))
route('/api/v1/documents','get','Active document list',arr(ref('Document')),params=[param('source_kind'),param('query')]+page)
route('/api/v1/documents','post','Create upload document version',ref('DocumentWrite'),201,[key],ref('DocumentInput'),'600 KiB request body; ETag is new version ID. Failed indexing is visible and never activated as current evidence.')
route('/api/v1/documents/{id}','put','Replace document with new version',ref('DocumentWrite'),200,[match],ref('DocumentInput'),'600 KiB request body; ETag is new version ID. Historical versions remain readable.')
route('/api/v1/documents/{id}','delete','Tombstone current document',ref('Document'),params=[match],desc='Exclude current version from retrieval; retain historical evidence.')
paths['/api/v1/documents/{id}']['delete']['responses']['202']={'description':'SQL tombstone committed; current retrieval excluded; index_status=cleanup_pending and durable cleanup outbox retries until removed. Historical source/chunks/evidence retained.','content':{'application/json':{'schema':envelope(ref('Document'))}}}
route('/api/v1/documents/{id}/versions','get','Historical version list',arr(ref('Version')),params=page)
route('/api/v1/documents/{id}/versions/{version}','get','Exact historical Markdown and chunks',ref('Version'))
confirm=obj({'confirm':{'type':'boolean','const':True}})
route('/api/v1/knowledge/reindex-demo','post','Explicit demo reindex',obj({'documents':typ('int'),'versions':typ('int')}),body=confirm,desc='Explicit confirm=true required; no general collection deletion.')
route('/api/v1/incidents/{id}/topology','get','Configured read-only service topology',ref('TopologySnapshot'),params=[param('environment'),param('service')],desc='Filters must equal incident scope. Missing config returns capability=not_configured and empty arrays. Snapshot source, validity and provenance are explicit. Potential impact is inference, not incident evidence; historical_evidence=false.')
route('/api/v1/incidents/{id}/metrics','get','Controlled Prometheus range query',ref('metricResult'),params=[param('metric_id',schema={'type':'string','enum':['error_rate','request_rate','latency_p99']},required=True),param('start',schema={'type':'string','format':'date-time'},required=True),param('end',schema={'type':'string','format':'date-time'},required=True),param('step',schema={'type':'integer','minimum':15,'maximum':86400},required=True)],desc='Unknown/repeated query parameters rejected. Ordered window <=24 hours; <=2000 timestamps; <=20 series; upstream timeout 10s and body <=2 MiB. Only configured scope templates, no arbitrary PromQL. Missing config returns not_configured empty series. NaN/Inf/missing samples become null, partial data_state; unsupported histograms/matrix shape return 422. Auxiliary live data is not historical evidence.')
paths['/api/v1/incidents/{id}/metrics']['get']['responses']['422']={'description':'unsupported_result_type','content':{'application/json':{'schema':ref('Error')}}}
# Auth uses a smaller meta envelope than the workspace resources.
authdata=obj({'authenticated':typ('bool'),'csrf_token':typ('string'),'expires_at':typ('*string'),'capabilities':arr(typ('string'))})
authmeta=obj({'request_id':typ('string'),'as_of':typ('string'),'data_state':typ('string'),'mode':typ('string')})
for method,description,data in [('post','Establish browser session using console bearer; Set-Cookie HttpOnly SameSite=Strict; secure except explicit localhost HTTP. Five login attempts/minute/IP. TTL 8 hours.',authdata),('get','Current console authentication; bearer has empty CSRF token and null expiry.',authdata),('delete','Clear session; cookie writes need same-origin Origin and X-CSRF-Token.',obj({'authenticated':{'type':'boolean','const':False}}))]:
 route('/api/v1/auth/session',method,'Console session',obj({'data':data,'meta':authmeta}),legacy=True,desc=description)
paths['/api/v1/auth/session']['post']['security']=[{'ConsoleBearer':[]}]
# Legacy routes deliberately do not use data/meta.
route('/alert','post','Admit Alertmanager observations',code=202,body={'type':'object','description':'Alertmanager webhook alerts array (<=100) with labels.alertname, status firing/resolved, startsAt/endsAt RFC3339; legacy name/severity/description observations also supported.'},legacy=True,desc='<=1 MiB. Webhook or console scope. Firing yields id/status/received/duplicate/dispatch_state; duplicate retains same run ID even while queue unavailable. Pure resolved yields 200 {status:accepted,received,diagnosis_enqueued:false}, no diagnosis.')
paths['/alert']['post']['security']=[{'WebhookBearer':[]},{'ConsoleBearer':[]},{'ConsoleCookie':[]}]
paths['/alert']['post']['responses']['200']={'description':'Resolved observation accepted; diagnosis_enqueued=false','content':{'application/json':{'schema':{'type':'object'}}}}
for path,method,desc,data,body,params in [('/reports','get','SQLite report compatibility projection; original migrated report archives retained; no data/meta.',obj({'reports':arr({'type':'object'}),'count':typ('int')}),None,None),('/upload','post','Upload namespace only; derived idempotency key when absent; updates create historical versions.',obj({'title':typ('string'),'count':typ('int'),'document_id':typ('string'),'active_version_id':typ('*string')}),ref('DocumentInput'),None),('/list','get','Current active document titles.',obj({'titles':arr(typ('string')),'count':typ('int')}),None,None),('/delete','delete','Delete uniquely matched upload title; ambiguous titles conflict; demo namespace protected.',obj({'title':typ('string'),'count':typ('int'),'index_status':typ('string')}),None,[param('title',required=True)]),('/reindex','post','Explicit demo indexing.',obj({'count':typ('int')}),confirm,None),('/chat','post','Stateless chat: <=32 KiB body, <=12 KiB message, 30s deadline. Only deterministic qualified current source excerpts; no arbitrary generated remediation. No evidence => safe human-review message; source failure503; deadline504; cancellation408.',obj({'reply':typ('string'),'citations':arr(ref('Citation')),'evidence_status':{'type':'string','enum':['has_citations','no_evidence']},'history_mode':{'type':'string','const':'stateless'}}),obj({'message':typ('string'),'session_id':typ('string')},['message']),None),('/session','delete','Stateless compatibility clear.',obj({'cleared':{'type':'integer','const':0},'history_mode':{'type':'string','const':'stateless'}}),None,None),('/plan','get','Legacy synchronous diagnostic path, 30s. Empty firing alerts return alerts/diagnosis/citations; otherwise durable report projection; timeout keeps accepted run queryable.',{'type':'object'},None,None)]:route(path,method,desc.split('.')[0],data,body=body,params=params,desc=desc,legacy=True)
paths['/delete']['delete']['responses']['202']={'description':'Upload document tombstoned; index_status=cleanup_pending until durable outbox cleanup succeeds.','content':{'application/json':{'schema':obj({'title':typ('string'),'count':typ('int'),'index_status':typ('string')})}}}
for p,d in [('/ping',obj({'status':{'type':'string','const':'ok'}})),('/ready',obj({'ready':typ('bool')}))]:
 route(p,'get','Public health probe',d,legacy=True);paths[p]['get']['security']=[]
route('/metrics','get','Protected Prometheus exposition',legacy=True)
paths['/metrics']['get']['responses']['200']['content']={'text/plain':{'schema':typ('string')}}
for method in ['get','post','delete']:
 route('/mcp',method,'MCP Streamable HTTP transport',legacy=True,desc='Read-only whitelist tools; protocol response JSON or text/event-stream. Console authentication required; underlying MCP SDK owns JSON-RPC schemas, session headers, negotiation and status codes.')
 paths['/mcp'][method]['responses']['200']['content']={'application/json':{'schema':{}},'text/event-stream':{'schema':typ('string')}}
spec={'openapi':'3.1.0','info':{'title':'Oncall Evidence Workspace HTTP API','version':'M2','description':'Implementation-bound contract. Workspace DTOs snake_case; Alertmanager timestamps and migrated legacy archive retain original spelling. Bearer console OR oncall_session cookie. Cookie mutations require matching Origin and X-CSRF-Token; webhook bearer limited to POST /alert. Missing token configuration fails closed503. Ordinary reads120/minute/identity. Protected /mcp and /metrics share authentication. No endpoint confirms, silences or remediates alerts. M3 global graph deferred; absent from paths. Auth/legacy errors can be smaller {error,...}; detailed uniform Error applies to workspace handler errors.'},'servers':[{'url':'/'}],'security':[{'ConsoleBearer':[]},{'ConsoleCookie':[]}],'paths':paths,'components':{'securitySchemes':{'ConsoleBearer':{'type':'http','scheme':'bearer','description':'Console token read from configured environment variable'},'WebhookBearer':{'type':'http','scheme':'bearer','description':'Separate webhook token, POST /alert only'},'ConsoleCookie':{'type':'apiKey','in':'cookie','name':'oncall_session'}},'schemas':S}}
# Cookie CSRF is conditional and must not be modeled as a required header for bearer clients.
S['Dependency']=obj({'data_state':typ('string'),'last_success_at':typ('*string')})
S['SystemStatus']=obj({'capabilities':obj({k:typ('string') for k in ['workspace','knowledge','topology','metrics','global_graph']}),'dependencies':obj({k:ref('Dependency') for k in ['sqlite','queue','retrieval','prometheus']}),'config_fingerprint':typ('string'),'auto_ingest':{'type':'boolean','const':False},'chat_history':{'type':'string','const':'stateless'}})
paths['/api/v1/system/status']['get']['responses']['200']['content']['application/json']['schema']=envelope(ref('SystemStatus'))
S['LegacyReport']=obj({'id':typ('string'),'status':typ('string'),'received_at':typ('string'),'alerts':arr(ref('Observation')),'diagnosis':typ('string'),'citations':arr({'type':'object'}),'score':typ('int'),'low_score':typ('bool'),'ingested':typ('int'),'evidence_status':typ('string'),'notification_status':typ('string')},['id','status'])
S['LegacyReport']['description']='Current report projection uses score=0 for no judge score and status=done for succeeded. Migrated historical archives retain original fields plus legacy_incomplete=true/evidence_status=not_evaluated; old citations are unqualified historical text.'
paths['/reports']['get']['responses']['200']['content']['application/json']['schema']=obj({'reports':arr(ref('LegacyReport')),'count':typ('int')})
paths['/chat']['post']['responses']['408']={'description':'Request cancelled','content':{'application/json':{'schema':ref('AuthError')}}}
spec['x-cookie-write-requirements']={'Origin':'must match request scheme and host','X-CSRF-Token':'must match current authenticated session'}
pathlib.Path('docs/api/workspace.openapi.yaml').write_text(yaml.safe_dump(spec,allow_unicode=True,sort_keys=False))
print('paths',len(paths),'operations',sum(len(v) for v in paths.values()),'schemas',len(S))
