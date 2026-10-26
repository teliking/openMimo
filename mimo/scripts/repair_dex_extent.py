"""Restore the readable extent needed by code items relocated by the shell.

Only derived copies are changed. Original runtime captures stay intact.
"""
import argparse
import json
import struct
import hashlib
import zlib
from pathlib import Path

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('names',nargs='+')
args = parser.parse_args()
captures = root/'analysis/runtime-dex-root'
outer_path = next(captures.rglob('*-263698104.dex'))
outer = outer_path.read_bytes()
outer_address = int(outer_path.name.split('-')[0],16)
destination = root/'analysis/extent-dex'
destination.mkdir(exist_ok=True)
records=[]
for name in args.names:
    path = next(captures.rglob(name))
    base = int(path.name.split('-')[0],16)-outer_address
    data = outer[base:]
    def u32(pos):
        return struct.unpack_from('<I',data,pos)[0]
    def leb(pos):
        value=0; shift=0
        while True:
            b=data[pos]; pos+=1; value|=(b&127)<<shift
            if b<128: return value,pos
            shift+=7
    original_size=u32(32)
    extent=original_size
    outside=0; total=0
    for i in range(u32(96)):
        pos=u32(u32(100)+i*32+24)
        if not pos: continue
        counts=[]
        for _ in range(4):
            value,pos=leb(pos); counts.append(value)
        for _ in range(counts[0]+counts[1]):
            _,pos=leb(pos); _,pos=leb(pos)
        for _ in range(counts[2]+counts[3]):
            _,pos=leb(pos); _,pos=leb(pos); code,pos=leb(pos)
            if code:
                total+=1
                outside+=code>=original_size
                insns_size=u32(code+12)
                if insns_size>1000000: raise ValueError(f'Implausible code item {code:x}')
                extent=max(extent,code+16+insns_size*2+4096)
    extent=min(extent,len(data))
    fixed=bytearray(data[:extent])
    struct.pack_into('<I',fixed,32,len(fixed))
    fixed[12:32]=hashlib.sha1(fixed[32:]).digest()
    struct.pack_into('<I',fixed,8,zlib.adler32(fixed[12:]) & 0xffffffff)
    (destination/name).write_bytes(fixed)
    record={'file':name,'original_size':original_size,'extended_size':extent,
            'methods':total,'relocated_methods':outside}
    records.append(record); print(record)
(destination/'extent-index.json').write_text(json.dumps(records,indent=2),encoding='utf-8')
