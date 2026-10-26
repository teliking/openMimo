import subprocess
from collections import deque
from pathlib import Path

root = Path(__file__).resolve().parents[1]
p = subprocess.Popen(['E:/Android/build-tools/36.0.0/aapt.exe','dump','--values','resources',
                      str(next(root.glob('*.apk')))], stdout=subprocess.PIPE, text=True,
                     encoding='utf-8', errors='replace')
remaining = 0
lines = []
previous = deque(maxlen=2)
for line in p.stdout:
    if any(s in line for s in ['string/hg212_product_name:', 'string/hg2125_product_name:', 'string/hg214_product_name:', 'string/hg224_product_name:']):
        remaining = 5
    if remaining:
        lines.append(line.rstrip())
        remaining -= 1
    if 'Pocket 3' in line:
        lines.extend(previous)
        lines.append(line.rstrip())
    previous.append(line.rstrip())
p.wait()
(root/'analysis/product-name-resources.txt').write_text('\n'.join(lines),encoding='utf-8')
print('\n'.join(lines[:36]))
