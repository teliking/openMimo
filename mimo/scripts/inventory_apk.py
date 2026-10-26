"""Record an APK baseline and extract only the initial analysis targets."""
import hashlib
import json
import struct
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
apk = next(ROOT.glob('*.apk'))
out = ROOT / 'analysis'
out.mkdir(exist_ok=True)
targets = {'classes.dex', 'AndroidManifest.xml', 'lib/arm64-v8a/libappsec.so',
           'lib/arm64-v8a/libAppGuard.so', 'lib/arm64-v8a/libdjisdk_jni.so',
           'lib/arm64-v8a/libdjibase.so', 'lib/arm64-v8a/libgimbal-lib.so',
           'lib/arm64-v8a/libStringFogJNI.so'}
with apk.open('rb') as f:
    digest = hashlib.file_digest(f, 'sha256').hexdigest()
with zipfile.ZipFile(apk) as z:
    entries = [{'name': i.filename, 'size': i.file_size, 'compressed_size': i.compress_size}
               for i in z.infolist()]
    for name in sorted(targets):
        destination = ROOT / 'extracted' / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(z.read(name))
    dex = z.read('classes.dex')
    count, offset = struct.unpack_from('<II', dex, 56)
    strings = []
    for index in range(count):
        pos = struct.unpack_from('<I', dex, offset + index * 4)[0]
        while dex[pos] & 128:
            pos += 1
        pos += 1
        end = dex.index(0, pos)
        strings.append(dex[pos:end].decode('utf-8', errors='replace'))
    (out / 'shell-dex-strings.txt').write_text('\n'.join(strings), encoding='utf-8')
    summary = {'apk': apk.name, 'size': apk.stat().st_size, 'sha256': digest,
               'dex_string_count': count, 'dex_class_count': struct.unpack_from('<I', dex, 96)[0],
               'entries': entries}
    (out / 'apk-inventory.json').write_text(json.dumps(summary, indent=2), encoding='utf-8')
print(json.dumps({k: v for k, v in summary.items() if k != 'entries'}, indent=2))
