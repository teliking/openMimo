"""Execute recovered Java serializers independently of the Go codec.

Adapters retain the original fromBytes/toBytes/serializedLength method bodies;
Android Parcelable/JSON methods are omitted. All adapters stay in analysis/.
"""
import json
import re
import subprocess
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
SRC=ROOT/'analysis/sdk-repaired-jadx/sources'
WORK=ROOT/'analysis/java-reference'
SCHEMA=json.loads((ROOT/'protocol/sdkvalue/schema.json').read_text())
JDK=Path('C:/Users/admin/.jdks/jbr-17.0.14/bin')
source_dir=WORK/'src'
source_dir.mkdir(parents=True,exist_ok=True)

def body(source,signature):
    m=re.search(signature+r'\s*\{',source)
    if not m:raise ValueError(signature)
    start=m.end();depth=1
    for end in range(start,len(source)):
        if source[end]=='{':depth+=1
        if source[end]=='}':depth-=1
        if not depth:return source[start:end]
    raise ValueError('unclosed method')

def write(qualified,source):
    path=source_dir/Path(qualified.replace('.','/')).with_suffix('.java')
    path.parent.mkdir(parents=True,exist_ok=True);path.write_text(source,encoding='utf-8')

def original(qualified):
    relative=Path(qualified.replace('.','/')).with_suffix('.java')
    for directory in [SRC,ROOT/'analysis/fpv-repaired-jadx/sources',ROOT/'analysis/midware-jadx/sources']:
        path=directory/relative
        if path.exists():return path.read_text(encoding='utf-8')
    raise FileNotFoundError(qualified)

required=set()
def adapter(qualified):
    if qualified in required:return
    required.add(qualified)
    source=original(qualified)
    if qualified in SCHEMA['enums']:
        r_import=re.search(r'import ([\w.]+\.R);',source)
        if r_import:
            r_source=original(r_import.group(1))
            def constant(m):
                value=re.search(r'public static final int '+re.escape(m.group(1))+r' = (0x[0-9a-fA-F]+|\d+);',r_source)
                if not value:raise ValueError(m.group(0))
                return value.group(1)
            source=re.sub(r'R\.styleable\.(\w+)',constant,source)
            source=source.replace(r_import.group(0),'')
        write(qualified,source);return
    layout=SCHEMA['types'][qualified]
    package,name=qualified.rsplit('.',1)
    imports='\n'.join(line for line in re.findall(r'^import (?:dji\.sdk\.keyvalue\.value\.[\w.]+|java\.(?:util|nio)\.[\w.]+);$',source,re.M) if 'base.DJIValue' not in line)
    fields=[]
    for field in layout['fields']:
        match=re.search(r'^    ([\w.<>?, \[\]]+) '+re.escape(field['name'])+r';$',source,re.M)
        if not match:raise ValueError((qualified,field))
        fields.append('public '+match.group(1)+' '+field['name']+';')
        nested=field['type'].removeprefix('[]')
        if nested in SCHEMA['types']:adapter(nested)
        if 'enum' in field:adapter(field['enum'])
    methods=[]
    for signature in [r'public int fromBytes\(byte\[\] bArr, int i\)',r'public int toBytes\(byte\[\] bArr, int i\)',r'public int serializedLength\(\)']:
        content=body(source,signature)
        header=signature.replace('\\','')
        methods.append(header+' {'+content+'}')
    methods.append('public byte[] toBytes(){byte[] data=new byte[serializedLength()];toBytes(data,0);return data;}')
    write(qualified,'package '+package+';\n'+imports+'\npublic class '+name+' implements dji.sdk.keyvalue.value.ByteStream {\n'+'\n'.join(fields+methods)+'\n}')

for name in ['ByteStream','ByteResult','ByteStreamHelper']:
    qualified='dji.sdk.keyvalue.value.'+name
    source=original(qualified)
    source=source.replace('bArr[i] = bool.booleanValue();','bArr[i] = (byte)(bool.booleanValue() ? 1 : 0);')
    write(qualified,source)
# Retained enums mention this marker; it has no serialization role.
write('dji.jni.JNIProguardKeepTag','package dji.jni; public interface JNIProguardKeepTag {}')

fixtures=[
 {'name':'video-4k60','type':'VideoResolutionFrameRate','value':{'resolution':'RESOLUTION_3840x2160','frameRate':'RATE_60FPS'}},
 {'name':'video-4k120-slow4','type':'VideoResolutionFrameRateAndSpeedRatio','value':{'frameRateAndResolution':{'resolution':'RESOLUTION_3840x2160','frameRate':'RATE_120FPS'},'speedRatio':4}},
 {'name':'gimbal-relative','type':'GimbalAngleRotation','value':{'mode':'RELATIVE_ANGLE','pitch':-12.5,'roll':0.0,'yaw':45.125,'pitchIgnored':False,'rollIgnored':True,'yawIgnored':False,'duration':1.75,'jointReferenceUsed':False,'timeout':2500}},
 {'name':'gimbal-speed','type':'GimbalSpeedRotation','value':{'pitch':-30.0,'yaw':60.0,'roll':0.0,'ctrlInfo':{'disableReturnCenter':True}}},
 {'name':'gimbal-waypoint','type':'GimbalCustomPathPoint','value':{'position':{'pitch':12.5,'roll':0.0,'yaw':-179.9},'duration':123456,'stayTime':3500}},
 {'name':'camera-mode-range','type':'CameraWorkModeRangeMsg','value':{'value':['SHOOT_PHOTO','RECORD_VIDEO','UNKNOWN']}},
]
factories=[]
counter=0
def expression(typ,value,enum=None):
    global counter
    if typ.startswith('[]'):
        return 'java.util.Arrays.asList('+','.join(expression(typ[2:],v,enum) for v in value)+')'
    if enum:
        code=SCHEMA['enums'][enum][value] if isinstance(value,str) else value
        return enum+'.find('+str(code)+')'
    if typ=='bool':return str(value).lower()
    if typ=='int32':return str(value)
    if typ=='int64':return str(value)+'L'
    if typ=='float64':return repr(float(value))+'d'
    if typ=='string':return json.dumps(value)
    adapter(typ)
    counter+=1;name='make'+str(counter)
    lines=[typ+' v=new '+typ+'();']
    for field in SCHEMA['types'][typ]['fields']:
        lines.append('v.'+field['name']+'='+expression(field['type'],value[field['name']],field.get('enum'))+';')
    factories.append('static '+typ+' '+name+'(){'+''.join(lines)+'return v;}')
    return name+'()'

main=[]
for item in fixtures:
    qualified=next(n for n in SCHEMA['types'] if n.endswith('.'+item['type']))
    item['type']=qualified
    expr=expression(qualified,item['value'])
    main.append('System.out.println("'+item['name']+'\\t"+java.util.HexFormat.of().formatHex('+expr+'.toBytes()));')
write('Reference','public class Reference {'+''.join(factories)+'public static void main(String[]args){'+''.join(main)+'}}')
java_files=[str(p) for p in source_dir.rglob('*.java')]
(WORK/'classes').mkdir(exist_ok=True)
compiled=subprocess.run([str(JDK/'javac.exe'),'-J-Duser.language=en','-encoding','UTF-8','-d',str(WORK/'classes'),*java_files],capture_output=True)
(WORK/'compile.log').write_bytes(compiled.stdout+compiled.stderr)
if compiled.returncode:
    print(compiled.stderr.decode('utf-8',errors='replace')[:4000])
    raise RuntimeError('Java reference compilation failed; see analysis/java-reference/compile.log')
result=subprocess.check_output([str(JDK/'java.exe'),'-cp',str(WORK/'classes'),'Reference'],text=True)
values=dict(line.split('\t') for line in result.splitlines())
for item in fixtures:item['hex']=values[item['name']]
out=ROOT/'protocol/testdata/mimo-java-values.json'
out.write_text(json.dumps({'source_apk_sha256':SCHEMA['source_apk_sha256'],'vectors':fixtures},indent=2)+'\n',encoding='utf-8')
print(result)
