package dev.openmimo;

import android.Manifest;
import android.app.*;
import android.bluetooth.BluetoothAdapter;
import android.content.*;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.os.*;
import android.text.InputType;
import android.text.method.PasswordTransformationMethod;
import android.view.*;
import android.widget.*;
import java.util.*;
import org.json.*;

/** Android UI uses public platform APIs; protocol decisions stay in Go. */
public final class MainActivity extends Activity implements CameraService.Listener {
    private static final int BG=0xff0b1118,CARD=0xff14202b,INK=0xffedf3f7,MUTED=0xff8fa5b8,ACCENT=0xff5ae0c6,RED=0xffff7581;
    private final Handler main=new Handler(Looper.getMainLooper());
    private CameraService service;
    private boolean bound,visible;
    private LinearLayout page,nav;
    private FrameLayout body;
    private TextView status,linkInfo,telemetry,scriptInfo,logView,readback,selfTestView;
    private final ArrayList<View> cameraControls=new ArrayList<>();
    private final ArrayList<String> deviceAddresses=new ArrayList<>();
    private Spinner deviceSpinner;
    private EditText addressField,ssidField,passwordField,scriptEditor,countField,intervalField;
    private CheckBox skipTCP;
    private int tab=0,jogPitch,jogYaw,jogSpeed=120;
    private long telemetryUpdated;
    private final String[] tabs={"连接","拍摄","云台","任务","日志"};
    private String cameraText="录像状态：尚未读回",batteryText="电池：—",gimbalText="姿态：—";
    private String lastSelfTest="离线自检不连接相机，用于检查 Go 核心与 APK 集成。";
    private final ServiceConnection binding=new ServiceConnection(){
        public void onServiceConnected(ComponentName n,IBinder b){service=((CameraService.LocalBinder)b).service();if(visible)service.addListener(MainActivity.this);render();updateStatus();}
        public void onServiceDisconnected(ComponentName n){service=null;updateStatus();}
    };
    public void onCreate(Bundle state){
        super.onCreate(state);getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        LinearLayout root=new LinearLayout(this);root.setOrientation(LinearLayout.VERTICAL);root.setBackgroundColor(BG);
        LinearLayout top=new LinearLayout(this);top.setOrientation(LinearLayout.VERTICAL);top.setPadding(dp(22),dp(18),dp(22),dp(14));
        LinearLayout brand=new LinearLayout(this);brand.setGravity(Gravity.CENTER_VERTICAL);
        TextView title=text("openMimo",26,INK);title.setTypeface(null,Typeface.BOLD);brand.addView(title,new LinearLayout.LayoutParams(0,-2,1));
        TextView tag=text("GO CORE  /  0.1",11,ACCENT);tag.setLetterSpacing(.1f);brand.addView(tag);top.addView(brand);
        TextView sub=text("POCKET 3  ·  自由编排你的拍摄",12,MUTED);sub.setPadding(0,dp(4),0,dp(15));top.addView(sub);
        status=text("●  未连接",15,ACCENT);top.addView(status);linkInfo=text("准备连接相机",11,MUTED);top.addView(linkInfo);root.addView(top);
        body=new FrameLayout(this);root.addView(body,new LinearLayout.LayoutParams(-1,0,1));
        nav=new LinearLayout(this);nav.setPadding(dp(10),dp(8),dp(10),dp(12));nav.setBackgroundColor(CARD);root.addView(nav);
        setContentView(root);render();bound=bindService(new Intent(this,CameraService.class),binding,Context.BIND_AUTO_CREATE);
    }
    protected void onStart(){super.onStart();visible=true;if(service!=null)service.addListener(this);}
    protected void onStop(){savePage();visible=false;stopJog();if(service!=null)service.removeListener(this);super.onStop();}
    protected void onDestroy(){stopJog();if(bound){unbindService(binding);bound=false;}main.removeCallbacksAndMessages(null);super.onDestroy();}
    private int dp(float x){return (int)(x*getResources().getDisplayMetrics().density+.5f);}
    private TextView text(String value,int sp,int color){TextView v=new TextView(this);v.setText(value);v.setTextSize(sp);v.setTextColor(color);v.setLineSpacing(dp(3),1);return v;}
    private GradientDrawable shape(int color,int radius){GradientDrawable d=new GradientDrawable();d.setColor(color);d.setCornerRadius(dp(radius));return d;}
    private void render(){
        cameraControls.clear();deviceSpinner=null;telemetry=null;scriptInfo=null;logView=null;readback=null;selfTestView=null;scriptEditor=null;
        body.removeAllViews();ScrollView scroll=new ScrollView(this);scroll.setFillViewport(true);body.addView(scroll);
        page=new LinearLayout(this);page.setOrientation(LinearLayout.VERTICAL);page.setPadding(dp(18),dp(6),dp(18),dp(24));scroll.addView(page);
        nav.removeAllViews();for(int i=0;i<tabs.length;i++){final int index=i;Button b=new Button(this);b.setText(tabs[i]);b.setAllCaps(false);b.setTextSize(13);b.setTextColor(i==tab?BG:MUTED);b.setBackground(shape(i==tab?ACCENT:CARD,10));LinearLayout.LayoutParams lp=new LinearLayout.LayoutParams(0,dp(43),1);lp.setMargins(dp(3),0,dp(3),0);nav.addView(b,lp);b.setOnClickListener(v->{savePage();stopJog();tab=index;render();});}
        if(tab==0)connectionPage();else if(tab==1)cameraPage();else if(tab==2)gimbalPage();else if(tab==3)taskPage();else logsPage();
        updateStatus();
    }
    private void heading(String title,String desc){TextView t=text(title,25,INK);t.setTypeface(null,Typeface.BOLD);t.setPadding(dp(2),dp(12),0,dp(6));page.addView(t);TextView d=text(desc,13,MUTED);d.setPadding(dp(2),0,0,dp(18));page.addView(d);}
    private LinearLayout card(String title){LinearLayout c=new LinearLayout(this);c.setOrientation(LinearLayout.VERTICAL);c.setPadding(dp(17),dp(16),dp(17),dp(16));c.setBackground(shape(CARD,16));LinearLayout.LayoutParams lp=new LinearLayout.LayoutParams(-1,-2);lp.setMargins(0,0,0,dp(14));page.addView(c,lp);if(!title.isEmpty()){TextView t=text(title,16,INK);t.setTypeface(null,Typeface.BOLD);t.setPadding(0,0,0,dp(10));c.addView(t);}return c;}
    private void note(LinearLayout c,String value){TextView t=text(value,12,MUTED);t.setPadding(0,dp(4),0,dp(9));c.addView(t);}
    private EditText input(LinearLayout c,String hint,String value,int type){EditText e=new EditText(this);e.setSingleLine(true);e.setTextSize(14);e.setTextColor(INK);e.setHintTextColor(MUTED);e.setHint(hint);e.setInputType(type);e.setText(value);e.setContentDescription(hint);e.setPadding(dp(11),dp(10),dp(11),dp(10));e.setBackground(shape(BG,8));LinearLayout.LayoutParams lp=new LinearLayout.LayoutParams(-1,-2);lp.setMargins(0,0,0,dp(10));c.addView(e,lp);return e;}
    private Spinner select(LinearLayout c,String label,String[] values){TextView l=text(label,12,MUTED);c.addView(l);Spinner s=new Spinner(this);ArrayAdapter<String> adapter=new ArrayAdapter<>(this,android.R.layout.simple_spinner_dropdown_item,values);s.setAdapter(adapter);s.setContentDescription(label);LinearLayout.LayoutParams lp=new LinearLayout.LayoutParams(-1,dp(47));lp.setMargins(0,0,0,dp(7));c.addView(s,lp);return s;}
    private Button button(LinearLayout c,String title,boolean primary,boolean camera,Runnable action){Button b=new Button(this);b.setText(title);b.setAllCaps(false);b.setTextSize(14);b.setTextColor(primary?BG:INK);b.setBackground(shape(primary?ACCENT:0xff243442,10));LinearLayout.LayoutParams lp=new LinearLayout.LayoutParams(-1,dp(47));lp.setMargins(0,dp(5),0,dp(5));c.addView(b,lp);b.setOnClickListener(v->{try{action.run();}catch(Exception e){toast(e.getMessage()==null?"输入无效":e.getMessage());}});if(camera)cameraControls.add(b);return b;}
    private void toast(String s){Toast.makeText(this,s,Toast.LENGTH_LONG).show();}
    private boolean ready(){if(service==null){toast("服务尚未就绪");return false;}return true;}
    private void savePage(){if(tab==3&&scriptEditor!=null)getPreferences(0).edit().putString("script",scriptEditor.getText().toString()).apply();if(tab==0&&addressField!=null)address();}
    private boolean permissions(int kind){
        ArrayList<String> p=new ArrayList<>();
        if((kind&1)!=0){if(Build.VERSION.SDK_INT>=31){p.add(Manifest.permission.BLUETOOTH_SCAN);p.add(Manifest.permission.BLUETOOTH_CONNECT);}else p.add(Manifest.permission.ACCESS_FINE_LOCATION);}
        if((kind&2)!=0){if(Build.VERSION.SDK_INT>=33)p.add(Manifest.permission.NEARBY_WIFI_DEVICES);else p.add(Manifest.permission.ACCESS_FINE_LOCATION);}
        ArrayList<String> missing=new ArrayList<>();for(String name:p)if(checkSelfPermission(name)!=PackageManager.PERMISSION_GRANTED)missing.add(name);
        if(!missing.isEmpty()){requestPermissions(missing.toArray(new String[0]),10);return false;}
        if(Build.VERSION.SDK_INT>=33&&checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED&&!getPreferences(0).getBoolean("notificationAsked",false)){getPreferences(0).edit().putBoolean("notificationAsked",true).apply();requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},11);}
        return true;
    }
    public void onRequestPermissionsResult(int r,String[] p,int[] results){super.onRequestPermissionsResult(r,p,results);if(r==10)toast("权限已更新，点击连接按钮继续");}
    private String address(){String s=addressField==null?getPreferences(0).getString("address","192.168.2.1:9004"):addressField.getText().toString().trim();getPreferences(0).edit().putString("address",s).apply();return s;}
    private void connectionPage(){
        heading("连接你的 Pocket 3","蓝牙配对 → 相机 Wi-Fi → Go 控制会话");
        LinearLayout c=card("01  已连接相机热点");
        note(c,"如果已用 Mimo 配对，请退出 Mimo，并先在系统设置中连接相机 Wi-Fi。");
        addressField=input(c,"相机地址",getPreferences(0).getString("address","192.168.2.1:9004"),InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
        skipTCP=new CheckBox(this);skipTCP.setText("跳过 TCP 引导（连接诊断）");skipTCP.setTextSize(12);skipTCP.setTextColor(MUTED);c.addView(skipTCP);
        button(c,"连接当前 Wi-Fi",true,false,()->{if(ready()&&permissions(0))service.connectCurrent(address(),skipTCP.isChecked());});
        button(c,"断开连接",false,false,()->{if(ready())service.disconnect();});
        c=card("02  首次蓝牙配对");note(c,"开启相机，在此选择 Pocket 3；相机提示配对时，请在相机屏幕确认。");
        button(c,"扫描 Pocket / DJI",false,false,()->{if(ready()&&permissions(1))service.scan();});
        deviceSpinner=select(c,"发现的设备",new String[]{"尚未扫描"});refreshDevices();
        button(c,"配对并连接",true,false,()->{if(!ready()||!permissions(3))return;int i=deviceSpinner.getSelectedItemPosition();if(i<0||i>=deviceAddresses.size()){toast("请先选择相机");return;}service.pair(deviceAddresses.get(i),address(),skipTCP.isChecked());});
        c=card("手动加入相机 Wi-Fi");
        ssidField=input(c,"相机 SSID","",InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
        passwordField=input(c,"Wi-Fi 密码","",InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_VARIATION_PASSWORD);passwordField.setTransformationMethod(PasswordTransformationMethod.getInstance());
        button(c,"加入 Wi-Fi 并连接",false,false,()->{if(ready()&&permissions(2))service.joinWifi(ssidField.getText().toString(),passwordField.getText().toString(),address(),skipTCP.isChecked());});
        c=card("开发版状态");selfTestView=text(lastSelfTest,12,MUTED);c.addView(selfTestView);
        button(c,"运行离线自检",false,false,()->{if(ready())service.call("self-test",null,result->{lastSelfTest=result.optBoolean("ok")?"6 项离线检查通过：CRC、录像格式、分辨率映射、V2 参数、输入校验、脚本校验。":"自检失败："+result.optString("error");if(selfTestView!=null)selfTestView.setText(lastSelfTest);service.event("result",lastSelfTest);});});
        note(c,"Pocket 3 协议开发版。相机端功能和固件兼容性仍需实测。视频预览与文件下载尚未接入。");
    }
    private void refreshDevices(){
        if(deviceSpinner==null)return;String old=null;int selected=deviceSpinner.getSelectedItemPosition();if(selected>=0&&selected<deviceAddresses.size())old=deviceAddresses.get(selected);
        deviceAddresses.clear();ArrayList<String> names=new ArrayList<>();if(service!=null)for(Map.Entry<String,String> entry:service.devices.entrySet()){deviceAddresses.add(entry.getKey());names.add(entry.getValue());}
        if(names.isEmpty())names.add("尚未发现相机");deviceSpinner.setAdapter(new ArrayAdapter<String>(this,android.R.layout.simple_spinner_dropdown_item,names));if(old!=null&&deviceAddresses.contains(old))deviceSpinner.setSelection(deviceAddresses.indexOf(old));
    }
    private void action(String type,Object... pairs){if(!ready())return;JSONObject o=NativeBridge.object("type",type);try{for(int i=0;i<pairs.length;i+=2)o.put((String)pairs[i],pairs[i+1]);}catch(Exception e){throw new IllegalArgumentException(e);}service.action(o);}
    private void cameraPage(){
        heading("拍摄与参数","更改一项，读回验证一项。相机状态优先于按钮状态。");
        LinearLayout c=card("相机状态");telemetry=text(cameraText+"\n"+batteryText+"\n"+gimbalText,14,INK);c.addView(telemetry);
        button(c,"开始录像",true,true,()->action("record-start"));Button stop=button(c,"停止录像",false,true,()->action("record-stop"));stop.setTextColor(RED);
        button(c,"拍一张",false,true,()->action("photo"));
        c=card("拍摄模式");int[] modes={1,5,0,40,2,24,10,12};Spinner mode=select(c,"模式",new String[]{"普通录像","照片","慢动作","低光录像","延时摄影","轨迹延时","移动延时","全景"});
        button(c,"应用拍摄模式",false,true,()->action("mode","mode",modes[mode.getSelectedItemPosition()]));
        c=card("录像格式");
        int[] resolutions={16,45,10,108,66,67,105,106,107};Spinner resolution=select(c,"分辨率 / 画幅",new String[]{"4K · 横屏","2.7K · 横屏","1080p · 横屏","3K · 竖屏","1080p · 竖屏","2.7K · 竖屏","1080p · 1:1","2160p · 1:1","3K · 1:1"});
        int[] rates={1,2,3,4,5,6,7,8};Spinner fps=select(c,"帧率",new String[]{"24 fps","25 fps","30 fps","48 fps","50 fps","60 fps","120 fps","240 fps"});fps.setSelection(5);
        int[] ratios={0,4,8};Spinner ratio=select(c,"慢动作倍率",new String[]{"普通录像","4×","8×"});
        button(c,"应用录像格式",true,true,()->{int res=resolutions[resolution.getSelectedItemPosition()],rate=rates[fps.getSelectedItemPosition()];if(!formatAllowed(res,rate))return;action("video-format","resolution",res,"fps",rate,"speed_ratio",ratios[ratio.getSelectedItemPosition()]);});
        button(c,"读取格式与能力表",false,true,()->{service.call("observe-property",NativeBridge.object("name","cam_video_param_v2"),r->showReadback(r));service.call("observe-property",NativeBridge.object("name","camcap_video_format"),r->showReadback(r));});
        readback=text("尚未读取相机能力。组合是否支持取决于当前固件与模式。",12,MUTED);c.addView(readback);
        Spinner codec=select(c,"编码格式",new String[]{"H.264","HEVC"});button(c,"应用编码格式",false,true,()->action("compression","enabled",codec.getSelectedItemPosition()==1));
        c=card("曝光与色彩");
        Spinner exposure=select(c,"曝光",new String[]{"自动曝光","手动曝光"});button(c,"应用曝光模式",false,true,()->action("exposure-manual","enabled",exposure.getSelectedItemPosition()==1));
        int[] isos={0,50,100,200,400,800,1600,3200,6400};Spinner iso=select(c,"手动 ISO",new String[]{"Auto","50","100","200","400","800","1600","3200","6400"});button(c,"应用 ISO",false,true,()->action("iso","iso",isos[iso.getSelectedItemPosition()]));
        int[] maxValues={100,200,400,800,1600,3200,6400};Spinner isoMax=select(c,"自动 ISO 上限",new String[]{"100","200","400","800","1600","3200","6400"});isoMax.setSelection(6);button(c,"应用 ISO 上限",false,true,()->action("iso-max","iso",maxValues[isoMax.getSelectedItemPosition()]));
        double[] shutters={1,2,4,8,15,25,30,50,60,100,120,200,240,500,1000,2000,4000,8000};String[] shutterLabels=new String[shutters.length];for(int i=0;i<shutters.length;i++)shutterLabels[i]=i==0?"1 秒":"1/"+(int)shutters[i]+" 秒";Spinner shutter=select(c,"手动快门",shutterLabels);shutter.setSelection(8);button(c,"应用快门",false,true,()->action("shutter","shutter_denominator",shutters[shutter.getSelectedItemPosition()]));
        String[] evLabels=new String[19];for(int i=0;i<19;i++)evLabels[i]=String.format(Locale.ROOT,"%+.1f EV",(i-9)/3.0);Spinner ev=select(c,"曝光补偿",evLabels);ev.setSelection(9);button(c,"应用 EV",false,true,()->action("ev","ev_thirds",ev.getSelectedItemPosition()-9));
        EditText kelvin=input(c,"白平衡 Kelvin（2000–10000）","5600",InputType.TYPE_CLASS_NUMBER);button(c,"应用自定义白平衡",false,true,()->action("white-balance","kelvin",Integer.parseInt(kelvin.getText().toString())));button(c,"自动白平衡",false,true,()->action("white-balance-auto"));
        int[] profiles={0,60,61};Spinner color=select(c,"色彩",new String[]{"Normal","HLG","D-Log M"});button(c,"应用色彩",false,true,()->action("color","profile",profiles[color.getSelectedItemPosition()]));
        Spinner focus=select(c,"对焦",new String[]{"AF-S 单次对焦","AF-C 连续对焦"});button(c,"应用对焦",false,true,()->action("focus-continuous","enabled",focus.getSelectedItemPosition()==1));
        button(c,"开启 Pro 参数",false,true,()->action("pro","enabled",true));
        c=card("照片与延时");Spinner raw=select(c,"照片格式",new String[]{"JPEG","JPEG + RAW"});button(c,"应用照片格式",false,true,()->action("photo-raw","enabled",raw.getSelectedItemPosition()==1));
        EditText interval=input(c,"延时间隔（0.1 秒单位）","10",InputType.TYPE_CLASS_NUMBER);EditText seconds=input(c,"延时时长（秒，0 为无限）","600",InputType.TYPE_CLASS_NUMBER);
        button(c,"应用延时参数",false,true,()->action("timelapse","interval_tenths",Integer.parseInt(interval.getText().toString()),"seconds",Long.parseLong(seconds.getText().toString()),"output",0));
        button(c,"全景 3×3",false,true,()->action("panorama-grid","enabled",true));button(c,"拍摄全景",false,true,()->action("panorama-shoot"));
    }
    private boolean formatAllowed(int res,int fps){
        if(service==null)return false;JSONObject p=service.properties.get("camcap_video_format");JSONObject d=p==null?null:p.optJSONObject("decoded");if(d==null||d.optInt("RawAvailability")==0)return true;
        JSONArray formats=d.optJSONArray("Formats");if(formats!=null)for(int i=0;i<formats.length();i++){JSONObject f=formats.optJSONObject(i);if(f!=null&&f.optInt("Resolution")==res&&f.optInt("FrameRate")==fps)return true;}
        toast("当前相机能力表未包含此分辨率 / 帧率组合");return false;
    }
    private void showReadback(JSONObject response){if(readback!=null&&response.optBoolean("ok")){try{readback.setText(response.optJSONObject("data").toString(2));}catch(Exception ignored){}}}
    private void gimbalPage(){
        heading("云台控制","按住移动，松开停止。应用另有 250 ms 指令看门狗。");
        LinearLayout c=card("手动方向");TextView speed=text("控制速度 120",13,MUTED);c.addView(speed);SeekBar slider=new SeekBar(this);slider.setMax(280);slider.setProgress(100);c.addView(slider);slider.setOnSeekBarChangeListener(new SeekBar.OnSeekBarChangeListener(){public void onProgressChanged(SeekBar s,int p,boolean u){jogSpeed=p+20;speed.setText("控制速度 "+jogSpeed);}public void onStartTrackingTouch(SeekBar s){}public void onStopTrackingTouch(SeekBar s){}});
        jogButton(c,"↑  向上",1,0);LinearLayout row=new LinearLayout(this);c.addView(row);jogButton(row,"← 左转",0,-1);Button stop=button(row,"停止",false,true,()->{stopJog();action("gimbal-stop");});stop.setTextColor(RED);jogButton(row,"右转 →",0,1);for(int i=0;i<row.getChildCount();i++)row.getChildAt(i).setLayoutParams(new LinearLayout.LayoutParams(0,dp(58),1));jogButton(c,"↓  向下",-1,0);
        button(c,"云台归中",true,true,()->action("gimbal-recenter"));
        c=card("绝对角度 · 实验");note(c,"已恢复 HG212 原生编码。坐标、限位与实际动作仍待接机确认，请先从小角度测试。");EditText yaw=input(c,"Yaw（度）","0",InputType.TYPE_CLASS_NUMBER|InputType.TYPE_NUMBER_FLAG_SIGNED|InputType.TYPE_NUMBER_FLAG_DECIMAL);EditText pitch=input(c,"Pitch（度）","0",InputType.TYPE_CLASS_NUMBER|InputType.TYPE_NUMBER_FLAG_SIGNED|InputType.TYPE_NUMBER_FLAG_DECIMAL);
        button(c,"转到指定角度",false,true,()->action("gimbal-angle","yaw",Math.round(Float.parseFloat(yaw.getText().toString())*10),"pitch",Math.round(Float.parseFloat(pitch.getText().toString())*10),"duration_ticks",10));
    }
    private void jogButton(LinearLayout c,String title,int pitch,int yaw){Button b=button(c,title,false,true,()->{});b.setOnTouchListener((v,event)->{if(!b.isEnabled())return false;switch(event.getActionMasked()){case MotionEvent.ACTION_DOWN:jogPitch=pitch;jogYaw=yaw;main.removeCallbacks(jogTick);jogTick.run();return true;case MotionEvent.ACTION_UP:case MotionEvent.ACTION_CANCEL:stopJog();v.performClick();return true;}return true;});}
    private final Runnable jogTick=new Runnable(){public void run(){if(service!=null&&(jogPitch!=0||jogYaw!=0)){service.jog(jogPitch*jogSpeed,jogYaw*jogSpeed);main.postDelayed(this,100);}}};
    private void stopJog(){main.removeCallbacks(jogTick);jogPitch=jogYaw=0;if(service!=null)service.jog(0,0);}
    private void taskPage(){
        heading("拍摄任务","Go 执行有限步骤；断线和错误停止后续动作，不自动重放。");
        LinearLayout c=card("运行状态");scriptInfo=text(service==null?"未运行":service.scriptPhase,15,ACCENT);c.addView(scriptInfo);button(c,"取消任务并停止云台",false,false,()->{if(ready()){service.call("script-stop",null,null);stopJog();}});note(c,"取消会停止后续步骤与云台。若任务已经开始录像，请另点“停止录像”。");
        c=card("间隔拍照");countField=input(c,"拍摄张数（1–1000）","10",InputType.TYPE_CLASS_NUMBER);intervalField=input(c,"每次拍照后等待（秒）","5",InputType.TYPE_CLASS_NUMBER|InputType.TYPE_NUMBER_FLAG_DECIMAL);
        button(c,"生成间隔拍照脚本",false,false,()->{try{int count=Integer.parseInt(countField.getText().toString());int ms=Math.round(Float.parseFloat(intervalField.getText().toString())*1000);JSONObject plan=NativeBridge.object("version",1,"name","间隔拍照","repeat",count,"steps",new JSONArray().put(NativeBridge.object("action",NativeBridge.object("type","photo"))).put(NativeBridge.object("wait_ms",ms)));scriptEditor.setText(plan.toString(2));}catch(Exception e){toast("请检查张数和等待时间");}});
        c=card("JSON 动作脚本");note(c,"支持拍照、录像、模式、参数、绝对角和限时云台移动。先校验，再运行。");scriptEditor=new EditText(this);scriptEditor.setTextSize(12);scriptEditor.setTextColor(INK);scriptEditor.setTypeface(Typeface.MONOSPACE);scriptEditor.setGravity(Gravity.TOP);scriptEditor.setInputType(InputType.TYPE_CLASS_TEXT|InputType.TYPE_TEXT_FLAG_MULTI_LINE|InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);scriptEditor.setMinLines(12);scriptEditor.setBackground(shape(BG,8));scriptEditor.setPadding(dp(10),dp(10),dp(10),dp(10));scriptEditor.setText(getPreferences(0).getString("script","{\n  \"version\": 1,\n  \"name\": \"间隔拍照\",\n  \"repeat\": 3,\n  \"steps\": [\n    {\"action\": {\"type\": \"photo\"}},\n    {\"wait_ms\": 5000}\n  ]\n}"));c.addView(scriptEditor);
        button(c,"校验脚本",false,false,()->script("script-validate"));button(c,"运行脚本",true,true,()->script("script-start"));
        button(c,"载入云台往返示例",false,false,()->{try{JSONObject plan=NativeBridge.object("version",1,"name","云台往返","repeat",2,"steps",new JSONArray().put(NativeBridge.object("action",NativeBridge.object("type","gimbal-jog","yaw",100,"duration_ms",1200))).put(NativeBridge.object("wait_ms",500)).put(NativeBridge.object("action",NativeBridge.object("type","gimbal-jog","yaw",-100,"duration_ms",1200))).put(NativeBridge.object("wait_ms",500)));scriptEditor.setText(plan.toString(2));}catch(Exception ignored){}});
    }
    private void script(String op){if(!ready())return;try{JSONObject plan=new JSONObject(scriptEditor.getText().toString());getPreferences(0).edit().putString("script",plan.toString()).apply();service.call(op,plan,result->{if(result.optBoolean("ok")){toast(op.equals("script-validate")?"脚本校验通过":"任务已提交");}});}catch(Exception e){toast("JSON 格式有误");}}
    private void logsPage(){heading("连接与运行日志","本地保留最近 180 条事件；不记录相机 Wi-Fi 密码。");LinearLayout c=card("事件记录");button(c,"复制日志",false,false,()->{if(ready()){android.content.ClipboardManager clipboard=getSystemService(android.content.ClipboardManager.class);clipboard.setPrimaryClip(ClipData.newPlainText("openMimo",String.join("\n",service.logs)));toast("日志已复制");}});button(c,"清空日志",false,false,()->{if(ready()){service.logs.clear();refreshLogs();}});button(c,"第三方许可",false,false,this::showLicenses);logView=text("",12,MUTED);logView.setTypeface(Typeface.MONOSPACE);logView.setTextIsSelectable(true);c.addView(logView);refreshLogs();}
    private void showLicenses(){try{StringBuilder all=new StringBuilder("openMimo 0.1.0\nGo runtime / Kaze for DJI references\n\n");for(String file:getAssets().list("licenses")){all.append(file).append("\n\n");try(java.io.InputStream in=getAssets().open("licenses/"+file)){java.io.ByteArrayOutputStream bytes=new java.io.ByteArrayOutputStream();byte[] buffer=new byte[4096];int n;while((n=in.read(buffer))>0)bytes.write(buffer,0,n);all.append(new String(bytes.toByteArray(),java.nio.charset.StandardCharsets.UTF_8)).append("\n\n");}}ScrollView scroll=new ScrollView(this);TextView value=text(all.toString(),12,INK);value.setPadding(dp(18),dp(10),dp(18),dp(10));value.setTextIsSelectable(true);scroll.addView(value);new AlertDialog.Builder(this).setTitle("第三方许可").setView(scroll).setPositiveButton("关闭",null).show();}catch(Exception e){toast("无法读取许可文件");}}
    private void refreshLogs(){if(logView!=null&&service!=null)logView.setText(String.join("\n",service.logs));}
    private void updateStatus(){
        String phase=service==null?"服务准备中":service.phase;status.setText("●  "+phase);status.setTextColor(service!=null&&service.connected?ACCENT:MUTED);
        boolean enabled=service!=null&&service.connected;for(View v:cameraControls){v.setEnabled(enabled);v.setAlpha(enabled?1:.4f);}
    }
    public void onEvent(String kind,Object data){
        if(kind.equals("devices"))refreshDevices();
        if(data instanceof JSONObject){JSONObject v=(JSONObject)data;
            if(kind.equals("camera"))cameraText="录像状态："+(v.optBoolean("Recording")?"录制中":v.optBoolean("Transition")?"切换中":"待机");
            if(kind.equals("battery"))batteryText="电池："+v.optInt("Percent")+"%";
            if(kind.equals("gimbal"))gimbalText=String.format(Locale.ROOT,"姿态：P %.1f° · Y %.1f°",v.optDouble("Pitch"),v.optDouble("Yaw"));
            if(kind.equals("link"))linkInfo.setText(v.optBoolean("healthy")?"控制链路正常 · DUML / UDP":"链路响应待确认");
            if(kind.equals("script-progress")&&scriptInfo!=null)scriptInfo.setText(v.optString("name")+" · 第 "+v.optInt("iteration")+" 轮 / 步骤 "+v.optInt("step"));
            if(kind.equals("property")&&readback!=null&&(v.optString("name").equals("cam_video_param_v2")||v.optString("name").equals("camcap_video_format"))){try{readback.setText(v.toString(2));}catch(Exception ignored){}}
        }
        if(telemetry!=null&&SystemClock.uptimeMillis()-telemetryUpdated>=250){telemetryUpdated=SystemClock.uptimeMillis();String value=cameraText+"\n"+batteryText+"\n"+gimbalText;if(!value.contentEquals(telemetry.getText()))telemetry.setText(value);}
        if(kind.equals("script-state")&&scriptInfo!=null)scriptInfo.setText(String.valueOf(data));
        if(kind.equals("error")||kind.equals("ble-error"))toast(String.valueOf(data));
        if(kind.equals("connection")&&!"connected".equals(data)){linkInfo.setText("控制会话未建立");cameraText="录像状态：当前未知";batteryText="电池：—";gimbalText="姿态：—";if(telemetry!=null)telemetry.setText(cameraText+"\n"+batteryText+"\n"+gimbalText);}
        if(Arrays.asList("connection","phase","wifi","ble-phase","ble-error","error").contains(kind))updateStatus();
        if(Arrays.asList("error","ble-error","ble-trace","connection","phase","wifi","ble-phase","action","result","script-state").contains(kind))refreshLogs();
    }
}
