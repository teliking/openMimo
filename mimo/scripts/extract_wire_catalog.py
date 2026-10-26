"""Recover command-set/command-id templates from the original native library.

TemplateTag is preserved without assigning it an unproven meaning. Receiver and
model support are deliberately not inferred from a command's name.
"""
import json
import re
from pathlib import Path

root=Path(__file__).resolve().parents[1]
symbols=json.loads((root/'analysis/sdk-symbols-restored.json').read_text())
records=[];seen=set();unparsed=[]
def name_at(text,previous=None):
    if text.startswith('v'):return 'void',text[1:]
    if text.startswith('S2_') and previous:return previous,text[3:]
    if text.startswith('NS0_'):
        value,tail=name_at(text[4:])
        if not tail.startswith('E'):raise ValueError(text)
        return 'dji::core::'+value,tail[1:]
    m=re.match(r'(\d+)',text)
    if not m:raise ValueError(text)
    count=int(m.group(1));start=m.end()
    return text[start:start+count],text[start+count:]
for item in symbols:
    m=re.match(r'_ZN3dji4core16dji_cmd_base_reqILh(\d+)ELh(\d+)EL([ht])(\d+)E',item['name'])
    if not m:continue
    try:
        request,tail=name_at(item['name'][m.end():])
        response,tail=name_at(tail,request)
    except ValueError:
        unparsed.append(item['name']);continue
    identity=(int(m.group(1)),int(m.group(2)),int(m.group(4)),request,response)
    if identity in seen:continue
    seen.add(identity)
    records.append({'template_tag':identity[0],'command_set':identity[1],'command_id':identity[2],
                    'request_type':request,'response_type':response,
                    'native_address':hex(item['address']),'native_symbol':item['name'],
                    'evidence':'apk-native-template','device_support':'not-established'})
records.sort(key=lambda r:(r['command_set'],r['command_id'],r['request_type']))
out=root/'protocol/wirecatalog';out.mkdir(parents=True,exist_ok=True)
(out/'catalog.json').write_text(json.dumps({'apk_sha256':'877cce18fb21cbdb6e025965e7eab4b06d823675428f61b14f641b719454c2a2',
                                          'commands':records,'unparsed_symbols':unparsed},indent=2)+'\n',encoding='utf-8')
print('Native command templates',len(records),'unparsed',len(unparsed))
