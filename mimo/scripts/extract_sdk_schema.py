"""Extract supported SDK ABI layouts and key metadata from recovered JADX sources.

These are Java/JNI value layouts, NOT camera wire-packet layouts.
Every extracted field is tied to a concrete ByteStreamHelper reader.
"""
import collections
import hashlib
import json
import re
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
SOURCES=ROOT/'analysis/sdk-repaired-jadx/sources'
DESTINATION=ROOT/'protocol/sdkvalue'
DESTINATION.mkdir(parents=True,exist_ok=True)

def method_body(source, signature):
    match=re.search(signature+r'\s*\{',source)
    if not match: return None
    start=match.end(); depth=1
    for end in range(start,len(source)):
        if source[end]=='{':depth+=1
        elif source[end]=='}':depth-=1
        if depth==0:return source[start:end]
    return None

readers={'d':'bool','n':'float64','w':'int32','C':'int64','I':'string','g':'bytes',
         'k':'[]float64','t':'[]int32','z':'[]int64','F':'[]string'}
schema={'source_apk_sha256':'877cce18fb21cbdb6e025965e7eab4b06d823675428f61b14f641b719454c2a2',
        'layer':'java-jni-value-abi','types':{},'enums':{},'unsupported':{}}
for path in sorted((SOURCES/'dji/sdk/keyvalue/value').rglob('*.java')):
    source=path.read_text(encoding='utf-8')
    package=re.search(r'^package ([\w.]+);',source,re.M)
    if not package:continue
    name=path.stem
    qualified=package.group(1)+'.'+name
    if re.search(r'public enum '+re.escape(name)+r'\b',source):
        pairs=re.findall(r'^    ([A-Z][A-Za-z0-9_]*)\(([^()\n,]+)\)[,;]',source,re.M)
        def enum_number(value):
            value=value.strip()
            if value=='Integer.MAX_VALUE':return 2147483647
            if re.fullmatch(r'-?(?:0x[0-9a-fA-F]+|\d+)',value):return int(value,0) if 'x' in value else int(value)
            ref=re.fullmatch(r'R\.styleable\.(\w+)',value)
            if ref:
                r_import=re.search(r'import ([\w.]+\.R);',source)
                r_source=(SOURCES/Path(r_import.group(1).replace('.','/')).with_suffix('.java')).read_text(encoding='utf-8')
                constant=re.search(r'public static final int '+re.escape(ref.group(1))+r' = (0x[0-9a-fA-F]+|\d+);',r_source)
                return int(constant.group(1),0)
            raise ValueError('Unparsed enum constant: '+qualified+' '+value)
        if pairs:schema['enums'][qualified]={n:enum_number(v) for n,v in pairs}
        continue
    body=method_body(source,r'public int fromBytes\(byte\[\] bArr, int i\)')
    if body is None:continue
    result_fields=[]; variables={}; failure=None
    # Turn the declaration matches into a field-name -> Java-type map.
    declarations={field:typ.strip() for typ,field in re.findall(r'^    (?:private |protected |public )?(?:final )?([\w.<>?, \[\]]+) (\w+);$',source,re.M)}
    imports={s.rsplit('.',1)[-1]:s for s in re.findall(r'^import ([\w.]+);',source,re.M)}
    def resolve(typ):return imports.get(typ,package.group(1)+'.'+typ)
    list_count=0
    for line in body.splitlines():
        result=re.search(r'\b(\w+) = ByteStreamHelper\.([A-Za-z])\(bArr, (.+)\);',line)
        if result:
            variable,helper,arguments=result.groups()
            typ=readers.get(helper)
            if helper in ('q','a'):
                cls=re.search(r'(\w+)\.class',arguments)
                if cls:typ=('[]' if helper=='a' else '')+resolve(cls.group(1))
            if not typ:failure='unrecognized reader '+helper;break
            variables[variable]=typ
        assignment=re.search(r'this\.(\w+) = (.+);',line)
        if assignment:
            field,expression=assignment.groups()
            if expression=='new ArrayList()':
                declared=declarations.get(field,'')
                element=re.fullmatch(r'List<(\w+)>',declared)
                add=re.search(r'this\.'+re.escape(field)+r'\.add\(([^;]+)\);',body)
                if not element or not add or '.getInt()' not in body:
                    failure='unrecognized list field '+field;break
                enum_match=re.search(r'(\w+)\.find\(',add.group(1))
                if enum_match:
                    result_fields.append({'name':field,'type':'[]int32','enum':resolve(enum_match.group(1))})
                    list_count+=1
                    continue
                failure='unrecognized list element '+field;break
            result=re.search(r'\b(\w+)\.a\b',expression)
            if not result or result.group(1) not in variables:
                failure='unrecognized field assignment '+field;break
            typ=variables[result.group(1)]
            enum=None
            enum_match=re.search(r'(\w+)\.find\(',expression)
            if enum_match:enum=resolve(enum_match.group(1))
            item={'name':field,'type':typ}
            if enum:item['enum']=enum
            result_fields.append(item)
    if not failure and len(result_fields)!=len(variables):failure='reader/field count differs'
    if not failure and not result_fields and body.strip()!='return i;':failure='nonempty unparsed layout'
    if failure:
        schema['unsupported'][qualified]=failure
    else:
        schema['types'][qualified]={'fields':result_fields,'source':str(path.relative_to(SOURCES)).replace('\\','/'),
                                   'source_sha256':hashlib.sha256(source.encode()).hexdigest()}

keys=[]
keydir=SOURCES/'dji/sdk/keyvalue/key'
for path in sorted(keydir.glob('DJI*Key.java')):
    source=path.read_text(encoding='utf-8')
    declarations={field:typ for typ,field in re.findall(r'public static final (DJI(?:Action)?KeyInfo<[^;]+>) (\w+);',source)}
    for field,kind,name,chain in re.findall(r'^\s*(\w+) = new (DJI(?:Action)?KeyInfo)\([^\n]*?"([^"]+)"([^\n]+);',source,re.M):
        flags={m: val=='true' for m,val in re.findall(r'\.([adbcj])\((true|false)\)',chain)}
        if set(flags)!={'a','d','b','c','j'}:continue
        keys.append({'class':path.stem,'field':field,'identifier':name,'java_type':declarations.get(field,''),
                     'can_get':flags['a'],'can_set':flags['d'],'can_listen':flags['b'],
                     'can_action':flags['c'],'event':flags['j'],
                     'source':str(path.relative_to(SOURCES)).replace('\\','/')})
schema['keys']=keys
(DESTINATION/'schema.json').write_text(json.dumps(schema,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print('types',len(schema['types']),'enums',len(schema['enums']),'keys',len(keys),'unparsed',len(schema['unsupported']))
print('unparsed reasons',collections.Counter(schema['unsupported'].values()).most_common(6))
