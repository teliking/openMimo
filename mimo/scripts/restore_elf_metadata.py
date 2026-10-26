"""Restore the actual PT_DYNAMIC file offset hidden by the protection wrapper.

The original file is retained. This changes metadata in an analysis copy only.
"""
import json
import struct
from pathlib import Path
from elftools.elf.elffile import ELFFile

root=Path(__file__).resolve().parents[1]
source=root/'extracted/lib/arm64-v8a/libdjisdk_jni.so'
data=bytearray(source.read_bytes())
phoff=struct.unpack_from('<Q',data,32)[0]
phnum=struct.unpack_from('<H',data,56)[0]
phentsize=struct.unpack_from('<H',data,54)[0]
headers=[struct.unpack_from('<II6Q',data,phoff+i*phentsize) for i in range(phnum)]
for i,h in enumerate(headers):
    if h[0]!=2: continue
    dynamic_address=h[3]
    containing=next(s for s in headers if s[0]==1 and s[3]<=dynamic_address<s[3]+s[5])
    file_offset=containing[2]+dynamic_address-containing[3]
    struct.pack_into('<Q',data,phoff+i*phentsize+8,file_offset)
struct.pack_into('<Q',data,40,0)
struct.pack_into('<HHH',data,58,0,0,0)
output=root/'analysis/libdjisdk_jni-static-restored.so'
output.write_bytes(data)
with output.open('rb') as f:
    elf=ELFFile(f)
    dynamic=next(s for s in elf.iter_segments() if s.header.p_type=='PT_DYNAMIC')
    symbols=[{'name':s.name,'address':s.entry.st_value,'size':s.entry.st_size}
             for s in dynamic.iter_symbols()]
(root/'analysis/sdk-symbols-restored.json').write_text(json.dumps(symbols,indent=2),encoding='utf-8')
print('Restored',len(symbols),'symbols:',output)
