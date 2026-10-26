"""Briefly stop only dji.mimo, collect runtime DEX, and always resume it."""
import subprocess
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ADB = ['adb', '-s', 'kf6x7l85cq6tpflv']

def adb(*args, **kwargs):
    return subprocess.run(ADB+list(args), check=True, **kwargs)

def root(command, **kwargs):
    return adb('shell', "su -c '" + command + "'", **kwargs)

adb('shell','am','force-stop','dji.mimo', capture_output=True)
adb('shell','am','start','-n','dji.mimo/dji.mimo.main.DJILaunchActivity',capture_output=True)
pid = None
for _ in range(40):
    probe = subprocess.run(ADB+['shell','pidof','dji.mimo'], capture_output=True, text=True)
    if probe.stdout.strip():
        pid = int(probe.stdout.split()[0]); break
    time.sleep(0.1)
if not pid:
    raise RuntimeError('Mimo did not start')
time.sleep(2)
print('Stopping research process',pid,flush=True)
root(f'kill -STOP {pid}')
try:
    maps = root(f'cat /proc/{pid}/maps',capture_output=True).stdout
    (ROOT/'analysis/runtime-maps.txt').write_bytes(maps)
    root(f'/data/local/tmp/mimo-read-dex {pid} /data/local/tmp/mimo-research-dex')
finally:
    subprocess.run(ADB+['shell',f"su -c 'kill -CONT {pid}'"])
root('chmod 755 /data/local/tmp/mimo-research-dex; chmod 644 /data/local/tmp/mimo-research-dex/*.dex')
adb('pull','/data/local/tmp/mimo-research-dex',str(ROOT/'analysis/runtime-dex-root'))
