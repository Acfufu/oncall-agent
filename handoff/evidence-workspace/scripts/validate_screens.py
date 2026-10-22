#!/usr/bin/env python3
"""Validate this handoff's image inventory; does not test a rendered application."""
from __future__ import annotations
import hashlib
import json
from pathlib import Path
import struct
import sys
import zlib
from html.parser import HTMLParser
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]

class LinkCollector(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.links: list[str] = []
        self.images: list[str] = []
    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if tag == 'a' and values.get('href'):
            self.links.append(values['href'])
        if tag == 'img' and values.get('src'):
            self.links.append(values['src'])
            self.images.append(values['src'])

def png_dimensions(data: bytes) -> tuple[int, int]:
    if data[:8] != b'\x89PNG\r\n\x1a\n':
        raise ValueError('Invalid PNG signature')
    offset = 8
    size = None
    saw_end = False
    while offset < len(data):
        if len(data) - offset < 12:
            raise ValueError('Truncated PNG chunk header')
        length = struct.unpack('>I', data[offset:offset + 4])[0]
        kind = data[offset + 4:offset + 8]
        end = offset + 12 + length
        if end > len(data):
            raise ValueError('Truncated PNG chunk')
        payload = data[offset + 8:offset + 8 + length]
        expected_crc = struct.unpack('>I', data[offset + 8 + length:end])[0]
        if (zlib.crc32(kind + payload) & 0xffffffff) != expected_crc:
            raise ValueError('PNG chunk CRC mismatch')
        if kind == b'IHDR':
            if length != 13:
                raise ValueError('Invalid IHDR length')
            size = struct.unpack('>II', payload[:8])
        if kind == b'IEND':
            saw_end = True
            if end != len(data):
                raise ValueError('Unexpected bytes after IEND')
            break
        offset = end
    if size is None or not saw_end:
        raise ValueError('Incomplete PNG file')
    return size

def validate() -> int:
    manifest = json.loads((ROOT / 'design/screens.json').read_text())
    plan = json.loads((ROOT / 'plan/tasks.json').read_text())
    sources = json.loads((ROOT / 'sources.json').read_text())
    rows = manifest['screens']
    errors: list[str] = []
    def check(condition: bool, message: str) -> None:
        if not condition:
            errors.append(message)
    check(len(rows) == manifest['reference_count'] == 7, 'Expected exactly seven screen references')
    check(len({s['id'] for s in rows}) == 7, 'Duplicate screen ID')
    check(len({s['path'] for s in rows}) == 7, 'Duplicate image path')
    check(len({s['sha256'] for s in rows}) == 7, 'Duplicate image content')
    check(manifest['handoff_version'] == plan['handoff_version'] == sources['handoff_version'] == '1.1', 'Version mismatch')
    task_map = {t['id']: t for t in plan['tasks']}
    source_map = {s['id']: s for s in sources['sources']}
    all_screen_ids = {s['id'] for s in rows}
    chapter = (ROOT / 'docs/10_SCREEN_REFERENCE_MAP.md').read_text()
    expected_images = set()
    for screen in rows:
        path = (ROOT / screen['path']).resolve()
        check(path.is_relative_to(ROOT), f'{screen["id"]}: path outside handoff')
        if not path.is_file():
            errors.append(f'{screen["id"]}: missing {path}')
            continue
        expected_images.add(path)
        data = path.read_bytes()
        check(hashlib.sha256(data).hexdigest() == screen['sha256'], f'{screen["id"]}: checksum mismatch')
        dimensions = png_dimensions(data)
        check(dimensions == (screen['width'], screen['height']) == (1586, 992), f'{screen["id"]}: dimensions mismatch')
        check(screen['id'] in chapter and screen['title'] in chapter, f'{screen["id"]}: missing page guidance')
        src = source_map.get(screen['source_id'], {})
        check(src.get('path') == screen['path'] and src.get('sha256') == screen['sha256'], f'{screen["id"]}: source metadata mismatch')
        check(bool(screen['task_ids']), f'{screen["id"]}: no implementation task links')
        for task_id in screen['task_ids']:
            check(task_id in task_map and screen['id'] in task_map.get(task_id, {}).get('reference_screen_ids', []), f'{screen["id"]}: missing reciprocal task link {task_id}')
        print(f'CHECK image CRC, SHA-256, dimensions, source and tasks: {screen["id"]} {screen["title"]}')
    actual_images = {p.resolve() for p in (ROOT / 'design/reference').glob('*.png')}
    check(actual_images == expected_images, 'Unindexed or missing PNG image')
    for task in plan['tasks']:
        check(set(task.get('reference_screen_ids', [])) <= all_screen_ids, f'{task["id"]}: unknown screen reference')
    for task_id in ('T13', 'T18', 'T22'):
        check('UI-05' in task_map[task_id]['acceptance_ids'], f'{task_id}: missing full-page acceptance gate')
    parser = LinkCollector()
    parser.feed((ROOT / 'design/gallery.html').read_text())
    check(len(parser.images) == 7, 'Gallery must display exactly seven original images')
    check({(ROOT / 'design' / p).resolve() for p in parser.images} == expected_images, 'Gallery image mapping mismatch')
    for target in parser.links:
        url = urlsplit(target)
        check(not url.scheme and not url.netloc, 'Offline gallery must not load external URLs')
        if url.path:
            check((ROOT / 'design' / unquote(url.path)).is_file(), f'Broken gallery link {target}')
    old = json.loads((ROOT / 'validation/baseline-v1.0-inventory.json').read_text())
    check(old['file_count'] == 41 and old['image_count'] == 1, 'Incorrect old inventory')
    unchanged_prefixes = ('contracts/', 'fixtures/', 'reference/audit/')
    for entry in old['files']:
        path = ROOT / entry['path']
        check(path.is_file(), f'Original file dropped: {entry["path"]}')
        if entry['path'].startswith(unchanged_prefixes) or entry['path'] == 'design/tokens.css':
            check(path.is_file() and hashlib.sha256(path.read_bytes()).hexdigest() == entry['sha256'], f'Preserved v1.0 artifact changed: {entry["path"]}')
    if errors:
        print('\nFAIL screen package validation:')
        for message in errors:
            print(' - ' + message)
        return 1
    print('\nPASS: seven unique original PNGs, file CRC/SHA-256, dimensions, sources, page guidance, task links and offline gallery.')
    print('PASS: all 41 v1.0 file paths retained; contracts, eight fixtures, audit reproductions and design tokens unchanged.')
    print('Scope: packaged design references only; NOT rendered application fidelity, runtime tests, mobile coverage or feature completion.')
    return 0

if __name__ == '__main__':
    try:
        sys.exit(validate())
    except (OSError, ValueError, KeyError, struct.error) as exc:
        print(f'ERROR: screen validation could not complete: {exc}', file=sys.stderr)
        sys.exit(2)
