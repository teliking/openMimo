param(
    [string]$SdkRoot = $(if ($env:ANDROID_HOME) {$env:ANDROID_HOME} else {'E:\Android'}),
    [string]$NdkRoot = $(if ($env:ANDROID_NDK_HOME) {$env:ANDROID_NDK_HOME} else {'D:\android-ndk-r29'}),
    [string]$JdkRoot = $(if ($env:JAVA_HOME) {$env:JAVA_HOME} else {'C:\Users\admin\.jdks\jbr-17.0.14'}),
    [string]$Go = 'E:\go\bin\go.exe',
    [switch]$Install,
    [string]$Device = 'kf6x7l85cq6tpflv'
)
$ErrorActionPreference='Stop'
$workspace=Split-Path -Parent $PSScriptRoot
$android=Join-Path $workspace 'android'
$build=Join-Path $android ('build/run-'+(Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
$native=Join-Path $android 'build/native'
$buildTools=Join-Path $SdkRoot 'build-tools/36.0.0'
$platform=Join-Path $SdkRoot 'platforms/android-35/android.jar'
$jdkBin=Join-Path $JdkRoot 'bin'
foreach($path in @($build,$native,(Join-Path $build 'classes'),(Join-Path $build 'dex'),(Join-Path $build 'generated'),(Join-Path $build 'staging/lib/arm64-v8a'),(Join-Path $android '.keys'),(Join-Path $workspace 'bin'))){New-Item -ItemType Directory -Force -Path $path | Out-Null}
function Invoke-Checked([string]$Program,[string[]]$Arguments){ & $Program @Arguments;if($LASTEXITCODE -ne 0){throw "$Program failed with exit code $LASTEXITCODE"} }
$priorOS=$env:GOOS;$priorArch=$env:GOARCH;$priorCgo=$env:CGO_ENABLED;$priorCC=$env:CC;$priorJava=$env:JAVA_HOME
Push-Location $workspace
try{
    $env:GOOS='android';$env:GOARCH='arm64';$env:CGO_ENABLED='1';$env:CC=Join-Path $NdkRoot 'toolchains/llvm/prebuilt/windows-x86_64/bin/aarch64-linux-android29-clang.cmd';$env:JAVA_HOME=$JdkRoot
    Invoke-Checked $Go @('build','-trimpath','-buildmode=c-shared','-ldflags','-s -w','-o',(Join-Path $native 'libmimo.so'),'./cmd/androidbridge')
    $env:GOOS=$priorOS;$env:GOARCH=$priorArch;$env:CGO_ENABLED=$priorCgo;$env:CC=$priorCC
    $sources=@(Get-ChildItem -LiteralPath (Join-Path $android 'src/main/java') -Filter '*.java' -Recurse | ForEach-Object {$_.FullName})
    $bootclasspath=$platform+';'+(Join-Path $buildTools 'core-lambda-stubs.jar')
    Invoke-Checked (Join-Path $jdkBin 'javac.exe') (@('-encoding','UTF-8','-source','8','-target','8','-bootclasspath',$bootclasspath,'-d',(Join-Path $build 'classes'))+$sources)
    Invoke-Checked (Join-Path $jdkBin 'jar.exe') @('--create','--file',(Join-Path $build 'classes.jar'),'-C',(Join-Path $build 'classes'),'.')
    Invoke-Checked (Join-Path $buildTools 'd8.bat') @('--lib',$platform,'--min-api','29','--output',(Join-Path $build 'dex'),(Join-Path $build 'classes.jar'))
    $unsigned=Join-Path $build 'unsigned.apk'
    Invoke-Checked (Join-Path $buildTools 'aapt.exe') @('package','-f','-M',(Join-Path $android 'src/main/AndroidManifest.xml'),'-S',(Join-Path $android 'src/main/res'),'-I',$platform,'-F',$unsigned,'-J',(Join-Path $build 'generated'))
    Copy-Item -LiteralPath (Join-Path $native 'libmimo.so') -Destination (Join-Path $build 'staging/lib/arm64-v8a/libmimo.so')
    $licenseAssets=Join-Path $build 'staging/assets/licenses'
    New-Item -ItemType Directory -Force -Path $licenseAssets | Out-Null
    Get-ChildItem -LiteralPath (Join-Path $workspace 'LICENSES') -Filter '*.txt' | ForEach-Object {Copy-Item -LiteralPath $_.FullName -Destination $licenseAssets}
    Invoke-Checked (Join-Path $jdkBin 'jar.exe') @('uf',$unsigned,'-C',(Join-Path $build 'dex'),'classes.dex','-C',(Join-Path $build 'staging'),'lib','-C',(Join-Path $build 'staging'),'assets')
    $aligned=Join-Path $build 'aligned.apk'
    Invoke-Checked (Join-Path $buildTools 'zipalign.exe') @('-P','16','-f','4',$unsigned,$aligned)
    $key=Join-Path $android '.keys/debug.keystore'
    if(-not (Test-Path -LiteralPath $key)){Invoke-Checked (Join-Path $jdkBin 'keytool.exe') @('-genkeypair','-keystore',$key,'-storepass','android','-keypass','android','-alias','openmimo-debug','-keyalg','RSA','-keysize','2048','-validity','10000','-dname','CN=openMimo Debug,O=Local Research,C=CN')}
    $apk=Join-Path $workspace 'bin/openmimo-0.1.0-arm64-debug.apk'
    Invoke-Checked (Join-Path $buildTools 'apksigner.bat') @('sign','--ks',$key,'--ks-key-alias','openmimo-debug','--ks-pass','pass:android','--key-pass','pass:android','--out',$apk,$aligned)
    Invoke-Checked (Join-Path $buildTools 'apksigner.bat') @('verify','--verbose',$apk)
    $report=[ordered]@{builtAt=(Get-Date).ToString('o');package='dev.openmimo';version='0.1.0';abi='arm64-v8a';minSdk=29;targetSdk=35;apk=$apk;sha256=(Get-FileHash -LiteralPath $apk -Algorithm SHA256).Hash;nativeSHA256=(Get-FileHash -LiteralPath (Join-Path $native 'libmimo.so') -Algorithm SHA256).Hash;buildDirectory=$build}
    $report | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $workspace 'analysis/android-build.json')
    if($Install){Invoke-Checked (Join-Path $SdkRoot 'platform-tools/adb.exe') @('-s',$Device,'install','-r',$apk)}
    $report | ConvertTo-Json
} finally {$env:GOOS=$priorOS;$env:GOARCH=$priorArch;$env:CGO_ENABLED=$priorCgo;$env:CC=$priorCC;$env:JAVA_HOME=$priorJava;Pop-Location}
