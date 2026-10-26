"""Read loaded DEX images from the dedicated Frida connection; no camera commands."""
import argparse
import hashlib
import json
import sys
import threading
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'tools/python'))
import frida

parser = argparse.ArgumentParser()
parser.add_argument('--pid', type=int, required=True)
args = parser.parse_args()
destination = ROOT / 'analysis/runtime-dex'
destination.mkdir(parents=True, exist_ok=True)
finished = threading.Event()
records = []

def receive(message, data):
    if message['type'] == 'send':
        payload = message['payload']
        if data:
            digest = hashlib.sha256(data).hexdigest()
            path = destination / f'{digest[:16]}.dex'
            path.write_bytes(data)
            payload['file'] = path.name
            payload['sha256'] = digest
            records.append(payload)
        print(json.dumps(payload), flush=True)
        if payload.get('event') == 'done':
            finished.set()
    else:
        print(json.dumps(message), flush=True)
        finished.set()

device = frida.get_device_manager().add_remote_device('127.0.0.1:27043')
session = device.attach(args.pid)
script = session.create_script(r"""
setImmediate(function () {
  const seen = new Set();
  let scanned = 0;
  for (const protection of ['r--', 'rw-']) {
    for (const range of Process.enumerateRanges({protection, coalesce: true})) {
      // File-backed framework images and large graphics allocations are irrelevant.
      const path = range.file ? range.file.path : '';
      if (path.startsWith('/system/') || path.startsWith('/apex/') ||
          path.startsWith('/vendor/') || path.startsWith('/dev/')) continue;
      if (range.size > 512 * 1024 * 1024) continue;
      scanned += range.size;
      let matches;
      try { matches = Memory.scanSync(range.base, range.size, '64 65 78 0a 30 ?? ?? 00'); }
      catch (_) { continue; }
      for (const match of matches) {
        const base = match.address;
        if (seen.has(base.toString())) continue;
        seen.add(base.toString());
        try {
          const size = base.add(32).readU32();
          const headerSize = base.add(36).readU32();
          const endian = base.add(40).readU32();
          if (headerSize !== 112 || endian !== 0x12345678 || size < 112 || size > 300*1024*1024) continue;
          send({event:'dex', address:base.toString(), size,
                classes:base.add(96).readU32(), source:path}, base.readByteArray(size));
        } catch (error) { send({event:'read-error',address:base.toString(),error:String(error)}); }
      }
    }
  }
  send({event:'done', scanned});
});
""")
script.on('message', receive)
session.on('detached', lambda *a: (print('detached', a, flush=True), finished.set()))
script.load()
finished.wait(180)
(destination / 'index.json').write_text(json.dumps(records, indent=2), encoding='utf-8')
session.detach()
