#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# Explicit isolated opt-in: no production compose, host volumes, or existing cleanup.
[ "${ONCALL_L2:-}" = 1 ] || { echo 'Set ONCALL_L2=1 to create isolated L2 containers'; exit 2; }
python3 - <<'PY'
import os, subprocess, uuid, json, tempfile, time, urllib.request, pathlib, socket
owner='oncall-l2-'+uuid.uuid4().hex
out=pathlib.Path('docs/execution/evidence-workspace/l2'); out.mkdir(parents=True,exist_ok=True)
manifest={'owner':owner,'containers':{},'profile':'real Redis/Qdrant/Prometheus; test HashEmbedder; no LLM'}
def run(*a): return subprocess.check_output(a,text=True).strip()
def save(): (out/'owner-manifest.json').write_text(json.dumps(manifest,indent=2))
def start(name,image,port,args=(),tmpfs='/data'):
 sock=socket.socket(); sock.bind(('127.0.0.1',0)); hostport=sock.getsockname()[1]; sock.close()
 cid=run('docker','run','-d','--label','oncall.l2.owner='+owner,'--name',owner+'-'+name,'-p',f'127.0.0.1:{hostport}:{port}','--tmpfs',tmpfs+':mode=1777',image,*args)
 manifest['containers'][name]={'id':cid,'image':image,'image_id':run('docker','inspect','--format','{{.Image}}',cid)}; save()
 mapping=run('docker','port',cid,f'{port}/tcp'); host=mapping.split(':')[-1]
 manifest['containers'][name]['port']=host; save(); return cid,host
try:
 redis,rport=start('redis','redis:7-alpine',6379,('redis-server','--save','','--appendonly','no'))
 qdrant,qport=start('qdrant','qdrant/qdrant:latest',6333,tmpfs='/qdrant/storage')
 prom,pport=start('prom','prom/prometheus:latest',9090,tmpfs='/prometheus')
 am,aport=start('alertmanager','prom/alertmanager:latest',9093,('--config.file=/etc/alertmanager/alertmanager.yml','--storage.path=/alertmanager','--cluster.listen-address=','--log.level=debug'),tmpfs='/alertmanager')
 cfg='''global:
  scrape_interval: 1s
scrape_configs:
- job_name: isolated-self
  static_configs:
  - targets: ['localhost:9090']
    labels: {environment: test, service: api}
  metric_relabel_configs:
  - source_labels: [__name__]
    regex: prometheus_http_requests_total
    target_label: __name__
    replacement: http_requests_total
  - source_labels: [code]
    target_label: status
'''
 with tempfile.NamedTemporaryFile(mode='w') as f:
  f.write(cfg); f.flush(); os.chmod(f.name,0o644); run('docker','cp',f.name,prom+':/etc/prometheus/prometheus.yml')
 run('docker','restart',prom)
 pport=run('docker','port',prom,'9090/tcp').split(':')[-1]; manifest['containers']['prom']['port']=pport; save()
 for name,port,path in [('qdrant',qport,'/readyz'),('prom',pport,'/-/ready'),('alertmanager',aport,'/-/ready')]:
  for i in range(60):
   try:
    with urllib.request.urlopen('http://127.0.0.1:'+port+path,timeout=1) as r:
     if r.status==200: break
   except Exception: time.sleep(.5)
  else: raise RuntimeError(name+' readiness timed out')
 env=dict(os.environ,ONCALL_L2='1',ONCALL_L2_REDIS='127.0.0.1:'+rport,ONCALL_L2_QDRANT='http://127.0.0.1:'+qport,ONCALL_L2_PROM='http://127.0.0.1:'+pport,ONCALL_L2_AM='http://127.0.0.1:'+aport,ONCALL_L2_OWNER=owner,ONCALL_L2_MANIFEST=str((out/'owner-manifest.json').resolve()))
 cmd=['go','test','./internal/handler','-run','^TestWorkspaceL2$','-count=1','-timeout','4m','-v']
 manifest['command']='ONCALL_L2=1 scripts/workspace-l2.sh -> '+' '.join(cmd); save()
 with (out/'run.log').open('w') as log:
  status=subprocess.call(cmd,env=env,stdout=log,stderr=subprocess.STDOUT)
 manifest['exit_code']=status; save()
 if status: raise SystemExit(status)
finally:
 manifest['cleanup']={}; cleanup_failed=False
 for name,c in manifest['containers'].items():
  cid=c['id']
  try:
   logs=subprocess.run(['docker','logs',cid],capture_output=True,text=True)
   (out/(name+'-container.log')).write_text(logs.stdout+logs.stderr)
   label=run('docker','inspect','--format','{{index .Config.Labels "oncall.l2.owner"}}',cid)
   if label != owner: raise RuntimeError('ownership mismatch')
   run('docker','rm','-f',cid); manifest['cleanup'][name]='removed matching owner ID'
  except Exception as e: manifest['cleanup'][name]=str(e); cleanup_failed=True
 save()
 if cleanup_failed: raise SystemExit('Owned resource cleanup failed; inspect owner-manifest.json')
PY
