import json,hashlib,subprocess,tempfile,threading,os,sqlite3
from pathlib import Path
from http.server import BaseHTTPRequestHandler,HTTPServer
base=Path('.omo/evidence/migration').resolve();fixture=Path(tempfile.mkdtemp(prefix='owned-cli-',dir=base));binary=base/'workspacectl';methods=[]
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  methods.append({'method':'GET','path':self.path});raw=json.dumps({'result':{'points_count':0,'config':{'params':{'vectors':{'size':64}}}}}).encode();self.send_response(200);self.end_headers();self.wfile.write(raw)
 def log_message(self,*args):pass
server=HTTPServer(('127.0.0.1',0),Handler);thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
legacy=fixture/'legacy.json';db=fixture/'owned.sqlite';backup=fixture/'prior.sqlite';cfg=fixture/'config.json';legacy.write_text(json.dumps([{'id':'retained-id','status':'done','received_at':'2026-10-01T12:00:00Z','alerts':[{'name':'CPU','labels':{'service':'api'}}],'diagnosis':'original unverified prose','citations':[{'doc':'unavailable-original.md','snippet':'historical original quote'}],'score':0}]))
cfg.write_text(json.dumps({'storage':{'sqlite_path':str(db)},'reports':{'persist_path':str(legacy)},'qdrant':{'host':'127.0.0.1','port':server.server_port,'collection':'owned-cli-fixture'}}));checksum=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();source_hash=checksum(legacy);config_hash=checksum(cfg);env=os.environ.copy();env['ONCALL_CONFIG']=str(cfg);log=[]
def invoke(*args,want=0):
 p=subprocess.run([str(binary),*args],env=env,capture_output=True,text=True);log.append({'invocation':[str(binary),*args],'ONCALL_CONFIG':str(cfg),'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr});assert p.returncode==want,(args,p.stdout,p.stderr);return json.loads(p.stdout) if p.returncode==0 else None
try:
 default_env=env.copy();default_env.pop('ONCALL_CONFIG',None);p=subprocess.run([str(binary),'inventory'],env=default_env,capture_output=True,text=True);assert p.returncode!=0;log.append({'invocation':[str(binary),'inventory'],'ONCALL_CONFIG':None,'exit':p.returncode,'stderr':p.stderr})
 v=invoke('dry-run');assert v['sqlite']['state']=='not_found' and not db.exists();invoke('import-legacy',want=1);assert not db.exists();v=invoke('import-legacy','--apply');assert v['imported']==1;v=invoke('import-legacy','--apply');assert v['duplicates']==1;db_hash=checksum(db);v=invoke('inventory');assert v['sqlite']['run_count']==1 and checksum(db)==db_hash
 invoke('backup','--output',str(backup));backup_hash=checksum(backup);invoke('backup','--output',str(backup),want=1);assert checksum(backup)==backup_hash and checksum(db)==db_hash
 later=fixture/'later.json';later.write_text(json.dumps([{'id':'post-backup','status':'failed','received_at':'2026-10-02T12:00:00Z','diagnosis':'later fact','score':2}]));invoke('import-legacy','--apply','--legacy',str(later));current_hash=checksum(db);v=invoke('inventory','--sqlite',str(backup));assert v['sqlite']['run_count']==1 and checksum(db)==current_hash
 conn=sqlite3.connect('file:'+str(backup)+'?mode=ro',uri=True);r=json.loads(conn.execute('select data from diagnosis_runs where id=?',('retained-id',)).fetchone()[0]);assert r['status']=='succeeded' and r['judge_score'] is None and r['citation_ids']==[];assert conn.execute('select count(*) from run_events').fetchone()[0]==0;assert conn.execute('select count(*) from outbox').fetchone()[0]==0;assert conn.execute('select count(*) from document_versions').fetchone()[0]==0;conn.close()
 assert checksum(legacy)==source_hash and checksum(cfg)==config_hash;assert all(m['method']=='GET' for m in methods)
 result={'status':'PASS','level':'L1 isolated temporary fixture + fake HTTP + actual CLI binary','fixture_dir':str(fixture),'source_sha256_before_after':source_hash,'config_sha256_before_after':config_hash,'qdrant_methods':methods,'invocations':log,'checks':['no-default-config','no-write-without-apply','dry-run-no-db-create','duplicate-import','inventory-source-unchanged','backup-no-overwrite','backup-selection-rollback','original-ID-score-null-raw-alerts-preserved','no-invented-events-outbox-docversions']}
 (base/'cli-scenarios.json').write_text(json.dumps(result,ensure_ascii=False,indent=2));print(json.dumps({'status':'PASS','artifact':str(base/'cli-scenarios.json')}))
finally:server.shutdown()
