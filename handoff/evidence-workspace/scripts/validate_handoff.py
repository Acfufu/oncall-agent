#!/usr/bin/env python3
"""Validate this handoff package, NOT the oncall-agent application.

Requires Python 3.10+ and jsonschema 4.x. Exits non-zero on any failure.
"""
from __future__ import annotations
import hashlib
import json
from pathlib import Path
import re
import sys
from datetime import datetime

ROOT = Path(__file__).resolve().parents[1]
ERRORS: list[str] = []
COUNT = 0

def check(condition: bool, message: str) -> None:
    global COUNT
    COUNT += 1
    if not condition:
        ERRORS.append(message)

def dt(s: str) -> datetime:
    return datetime.fromisoformat(s.replace('Z', '+00:00'))

def read_json(path: Path):
    return json.loads(path.read_text(encoding='utf-8'))

def validate() -> int:
    try:
        from jsonschema import Draft202012Validator, FormatChecker
    except ImportError:
        print('ERROR: jsonschema 4.x is required; schema checks were NOT run.', file=sys.stderr)
        print('Install it in an isolated environment, then rerun.', file=sys.stderr)
        return 2
    schema = read_json(ROOT/'contracts/workspace.schema.json')
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    fixture_paths = sorted((ROOT/'fixtures').glob('*.json'))
    check(len(fixture_paths) == 8, 'Expected exactly eight documented JSON fixtures')
    for p in fixture_paths:
        f = read_json(p)
        schema_errors = list(validator.iter_errors(f))
        check(not schema_errors, f'{p.name}: ' + '; '.join(e.message for e in schema_errors))
        if schema_errors:
            continue
        graph, run, incident = f['graph'], f['run'], f['incident']
        check(f['meta']['mode'] == 'demo', f'{p.name}: fixture must be explicitly demo')
        check(graph['run_id'] == run['id'], f'{p.name}: graph/run mismatch')
        check(graph['incident_id'] == incident['id'], f'{p.name}: incident mismatch')
        check(incident['id'] in run['incident_ids'], f'{p.name}: run not linked to incident')
        check(incident['latest_run_id'] == run['id'], f'{p.name}: latest run mismatch')
        eids = {e['id'] for e in f['evidence']}
        evid = {e['id']: e for e in f['evidence']}
        nids = {n['id'] for n in graph['nodes']}
        check(len(eids) == len(f['evidence']), f'{p.name}: duplicate evidence id')
        check(len(nids) == len(graph['nodes']), f'{p.name}: duplicate node id')
        check(len({e['id'] for e in graph['edges']}) == len(graph['edges']), f'{p.name}: duplicate edge id')
        for n in graph['nodes']:
            check(set(n['source_refs']) <= eids, f'{p.name}: node {n["id"]} missing source')
            if n['type'] == 'hypothesis':
                check(n['verification'] == 'unverified', f'{p.name}: synthetic hypothesis must remain unverified')
        for e in graph['edges']:
            check(e['source'] in nids and e['target'] in nids, f'{p.name}: dangling edge {e["id"]}')
            check(set(e['source_refs']) <= eids, f'{p.name}: missing edge provenance {e["id"]}')
            check(dt(e['recorded_at']) <= dt(f['meta']['as_of']), f'{p.name}: future edge')
            if e['basis'] == 'inferred':
                check(e['verification'] == 'unverified', f'{p.name}: inferred fixture edge is not unverified')
        for e in f['evidence']:
            check(dt(e['recorded_at']) <= dt(f['meta']['as_of']), f'{p.name}: future evidence')
            if e['observed_at']:
                check(dt(e['observed_at']) <= dt(e['recorded_at']), f'{p.name}: evidence recorded before observation')
            if e['kind'] == 'document_chunk':
                check(all(e[k] for k in ('document_id','version_id','chunk_id')), f'{p.name}: unversioned chunk')
        for cid in run['citation_ids']:
            check(cid in eids and evid[cid]['kind']=='document_chunk', f'{p.name}: invalid citation {cid}')
        if run['evidence_status'] == 'has_citations':
            check(bool(run['citation_ids']), f'{p.name}: missing citations')
        if run['evidence_status'] in ('no_evidence','source_unavailable'):
            check(not run['citation_ids'], f'{p.name}: unsafe evidence-state fixture')
        if run['status'] == 'failed':
            check(run['error'] is not None, f'{p.name}: failed run lacks error')
        if run['finished_at'] and run['started_at']:
            duration=(dt(run['finished_at'])-dt(run['started_at'])).total_seconds()*1000
            check(abs(duration-run['duration_ms']) < 0.01, f'{p.name}: run duration mismatch')
        if incident['lifecycle_status'] == 'resolved':
            check(incident['resolved_at'] is not None, f'{p.name}: resolved without timestamp')
            check(any(e['kind']=='alert_observation' and e['metadata'].get('status')=='resolved' for e in f['evidence']), f'{p.name}: resolved without separate source')
        seqs=[e['sequence'] for e in f['events']]
        check(seqs == sorted(set(seqs)), f'{p.name}: event sequence not unique/increasing')
        check(len({e['event_id'] for e in f['events']}) == len(f['events']), f'{p.name}: duplicate event id')
        last_time=None
        started={}
        for e in f['events']:
            check(e['run_id']==run['id'], f'{p.name}: cross-run event')
            check(e['result_ref'] is None or e['result_ref'] in eids, f'{p.name}: dangling event result')
            when=dt(e['occurred_at'])
            check(when <= dt(f['meta']['as_of']), f'{p.name}: future event')
            check(last_time is None or when >= last_time, f'{p.name}: fixture chronology reversed')
            last_time=when
            key=(e['attempt'], e['stage'], e['stage_attempt'])
            if e['kind']=='started':
                started[key]=when
            elif e['duration_ms'] is not None and key in started:
                measured=(when-started[key]).total_seconds()*1000
                check(abs(measured-e['duration_ms'])<0.01, f'{p.name}: stage duration mismatch {key}')
        check(graph['truncated'] == (sum(graph['omitted_counts'].values())>0), f'{p.name}: truncation mismatch')
        check(f['summary']['cited_succeeded_runs'] <= f['summary']['succeeded_runs'], f'{p.name}: invalid ratio')
        print(f'PASS fixture schema and semantic checks: {p.name}')

    plan=read_json(ROOT/'plan/tasks.json')
    tasks=plan['tasks']; ids={t['id'] for t in tasks}
    check(len(ids)==len(tasks)==24, 'Expected 24 distinct task ids')
    known_tests=set(re.findall(r'\| ([A-Z0-9]+-\d{2}) \|', (ROOT/'docs/06_ACCEPTANCE_TESTS.md').read_text()))
    for task in tasks:
        check(set(task['depends_on']) <= ids, f'{task["id"]}: unknown dependency')
        check(set(task['acceptance_ids']) <= known_tests, f'{task["id"]}: unknown acceptance test')
        check(task['status']=='pending', f'{task["id"]}: initial handoff must not claim implementation')
    remaining={t['id']:set(t['depends_on']) for t in tasks}
    order=[]
    while remaining:
        ready=sorted(t for t,d in remaining.items() if not d)
        if not ready:
            check(False,'Task graph has a cycle');break
        order.extend(ready)
        for t in ready:del remaining[t]
        for d in remaining.values():d.difference_update(ready)
    print('PASS task DAG: ' + ' -> '.join(order))

    sources=read_json(ROOT/'sources.json')
    source_ids={s['id'] for s in sources['sources']}
    check(sources['baseline_commit']==plan['baseline_commit'],'Baseline SHA mismatch')
    reference=ROOT/'design/reference/ui-concept-original.png'
    expected='2b05934fbe6a9adcb9de9eace7422114e6aa4160972b6b2dfe19e84f23b2e4ea'
    check(hashlib.sha256(reference.read_bytes()).hexdigest()==expected,'Original UI reference has changed')
    print('PASS original concept image checksum')

    # Only Markdown local links; code paths in backticks are not treated as links.
    for md in ROOT.rglob('*.md'):
        content=md.read_text(encoding='utf-8')
        for sid in re.findall(r'\[(SRC-\d+)\]',content):
            check(sid in source_ids, f'{md.relative_to(ROOT)}: unknown source {sid}')
        for target in re.findall(r'\]\(([^)]+)\)',content):
            target=target.split('#',1)[0]
            if not target or '://' in target or target.startswith(('mailto:','sandbox:')):
                continue
            check((md.parent/target).exists(), f'{md.relative_to(ROOT)}: broken link {target}')
    print('PASS local Markdown links and source IDs')

    manifest=ROOT/'validation/SHA256SUMS.txt'
    if manifest.exists():
        for line in manifest.read_text().splitlines():
            expected_hash,rel=line.split('  ',1)
            path=ROOT/rel
            check(path.exists(),f'Manifest missing file: {rel}')
            if path.exists():
                check(hashlib.sha256(path.read_bytes()).hexdigest()==expected_hash,f'Manifest hash mismatch: {rel}')
        print('PASS payload SHA-256 manifest')
    if ERRORS:
        print('\nFAILED package validation:')
        for error in ERRORS:print(' - '+error)
        print(f'{len(ERRORS)} failure(s), {COUNT} checks')
        return 1
    print(f'\nPASS: {COUNT} package-only checks; {len(fixture_paths)} fixtures; {len(tasks)} tasks.')
    print('This does NOT certify application implementation, application tests, visual fidelity, or model quality.')
    return 0

if __name__ == '__main__':
    try:
        sys.exit(validate())
    except (OSError, ValueError, KeyError) as exc:
        print(f'ERROR: package validation could not complete: {exc}',file=sys.stderr)
        sys.exit(2)
