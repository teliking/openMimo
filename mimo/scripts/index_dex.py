"""Build a searchable class-to-DEX index without decompiling every dependency."""
import collections
import hashlib
import json
import struct
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
records = []
lines = []
seen = set()
for path in sorted((ROOT/'analysis/runtime-dex-root').rglob('*.dex')):
    data = path.read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    if digest in seen:
        continue
    seen.add(digest)
    def u32(offset):
        return struct.unpack_from('<I',data,offset)[0]
    strings = []
    for i in range(u32(56)):
        pos = u32(u32(60)+4*i)
        while data[pos] & 128:
            pos += 1
        pos += 1
        strings.append(data[pos:data.index(0,pos)].decode('utf-8',errors='replace'))
    types = [strings[u32(u32(68)+i*4)] for i in range(u32(64))]
    classes = [types[u32(u32(100)+i*32)] for i in range(u32(96))]
    for cls in classes:
        lines.append(f'{cls}\t{path.name}')
    packages = collections.Counter('.'.join(s.strip('L;').split('/')[:3]) for s in classes)
    records.append({'file':path.name,'sha256':digest,'classes':len(classes),'bytes':len(data),
                    'top_packages':packages.most_common(8)})
(ROOT/'analysis/dex-index.json').write_text(json.dumps(records,indent=2),encoding='utf-8')
(ROOT/'analysis/class-index.tsv').write_text('\n'.join(sorted(lines)),encoding='utf-8')
print('Unique DEX:',len(records),'Total classes:',len(lines))
for r in records:
    if r['classes']>100:
        print(r['file'],r['classes'],r['top_packages'][:3])
