import argparse
import json
import re
from pathlib import Path

root=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser()
parser.add_argument('patterns',nargs='+')
parser.add_argument('--limit',type=int,default=60)
args=parser.parse_args()
symbols=json.loads((root/'analysis/sdk-symbols-restored.json').read_text())
matches=[s for s in symbols if any(re.search(p,s['name'],re.I) for p in args.patterns)]
print('matches',len(matches))
for s in matches[:args.limit]:
    print(hex(s['address']),s['size'],s['name'])
