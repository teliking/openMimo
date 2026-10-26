package dev.openmimo;

import android.app.*;
import android.bluetooth.*;
import android.bluetooth.le.*;
import android.content.*;
import android.os.*;
import java.text.SimpleDateFormat;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Consumer;
import org.json.*;

public final class CameraService extends Service {
    public interface Listener { void onEvent(String kind, Object data); }
    public final class LocalBinder extends Binder { CameraService service() { return CameraService.this; } }
    final Handler main = new Handler(Looper.getMainLooper());
    private final LocalBinder binder = new LocalBinder();
    private final ExecutorService workers = Executors.newFixedThreadPool(3);
    private final ExecutorService fast = Executors.newSingleThreadExecutor();
    private final ExecutorService polls = Executors.newSingleThreadExecutor();
    private final ExecutorService diagnostics = Executors.newSingleThreadExecutor();
    private final JSONObject latestTelemetry = new JSONObject();
    private final ArrayDeque<JSONObject> propertyReads = new ArrayDeque<>();
    private final ArrayList<Listener> listeners = new ArrayList<>();
    final ArrayDeque<String> logs = new ArrayDeque<>();
    final LinkedHashMap<String, String> devices = new LinkedHashMap<>();
    final LinkedHashMap<String, JSONObject> properties = new LinkedHashMap<>();
    volatile boolean connected, connecting, scriptRunning;
    volatile String phase = "未连接", scriptPhase = "未运行";
    String address = "192.168.2.1:9004";
    boolean skipTCP;
    private volatile boolean alive;
    private boolean foreground;
    private GattBridge gatt;
    private WifiLink wifi;
    private BluetoothLeScanner scanner;
    private PowerManager.WakeLock wake;
    private final Runnable scanTimeout=this::stopScan;

    public void onCreate() {
        super.onCreate(); alive = true;
        getSystemService(NotificationManager.class).createNotificationChannel(new NotificationChannel("control", "相机连接", NotificationManager.IMPORTANCE_LOW));
        wifi = new WifiLink(this);
        wake = getSystemService(PowerManager.class).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "openMimo:session");
        wake.setReferenceCounted(false);
        main.post(diagnosticSnapshot);
        polls.execute(() -> {
            while (alive) {
                JSONObject result = NativeBridge.call("poll", null);
                JSONArray events = result.optJSONArray("data");
                if (events != null) for (int i=0;i<events.length();i++) {
                    JSONObject event = events.optJSONObject(i); if (event != null) main.post(() -> handle(event));
                }
            }
        });
    }
    public IBinder onBind(Intent intent) { return binder; }
    public int onStartCommand(Intent intent, int flags, int startId) {
        startVisible();
        if (intent != null && "stop".equals(intent.getAction())) disconnect();
        return START_NOT_STICKY;
    }
    private void startVisible() {
        Intent open = new Intent(this, MainActivity.class);
        PendingIntent content = PendingIntent.getActivity(this, 0, open, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        Intent stop = new Intent(this, CameraService.class).setAction("stop");
        PendingIntent stopIntent = PendingIntent.getService(this, 1, stop, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        Notification notification = new Notification.Builder(this,"control").setSmallIcon(android.R.drawable.ic_menu_camera)
            .setContentTitle("openMimo 相机控制").setContentText("保持相机连接和拍摄任务").setContentIntent(content)
            .addAction(new Notification.Action.Builder(null,"断开",stopIntent).build()).setOngoing(true).build();
        startForeground(41,notification); foreground = true;
        wake.acquire(connected?2*60*60*1000L:60000L);
    }
    void ensureStarted() { startForegroundService(new Intent(this,CameraService.class)); }
    void addListener(Listener l) { if (!listeners.contains(l)) listeners.add(l); }
    void removeListener(Listener l) { listeners.remove(l); }
    void call(String op, Object params, Consumer<JSONObject> callback) {
        workers.execute(() -> {
            JSONObject result = NativeBridge.call(op, params);
            main.post(() -> { if (!alive) return;
                if(op.equals("read-property")||op.equals("observe-property")){propertyReads.addLast(NativeBridge.object("operation",op,"request",params,"response",result));while(propertyReads.size()>10)propertyReads.removeFirst();}
                if (!result.optBoolean("ok")) event("error",op+": "+result.optString("error")); if (callback != null) callback.accept(result); });
        });
    }
    void nativeFast(String op, Object params) { if (alive) fast.execute(() -> NativeBridge.call(op,params)); }
    void action(JSONObject value) { call("action",value,r -> { if (r.optBoolean("ok")) event("result", "已收到指令应答；状态以相机读回为准"); }); }
    void jog(int pitch, int yaw) { nativeFast("jog",NativeBridge.object("pitch",pitch,"yaw",yaw)); }
    void connectCurrent(String address, boolean skip) { if(connected||connecting){event("error","请先断开当前会话");return;}this.address=address;this.skipTCP=skip;ensureStarted();wifi.useCurrent(); }
    void joinWifi(String ssid,String password,String address,boolean skip) { if(connected||connecting){event("error","请先断开当前会话");return;}this.address=address;this.skipTCP=skip;ensureStarted();wifi.join(ssid,password); }
    void connectControl() {
        if (connected || connecting) return;
        connecting=true;phase="建立控制会话";event("phase",phase);
        call("connect",NativeBridge.object("address",address,"skip_tcp",skipTCP),r -> {if (!r.optBoolean("ok")) connecting=false;});
    }
    void networkLost() { event("error","相机 Wi-Fi 已断开");disconnect(); }
    void disconnect() {
        stopScan();jog(0,0);
        call("disconnect",null,r -> {
            connected=false;connecting=false;scriptRunning=false;properties.clear();
            if (gatt!=null) {gatt.close();gatt=null;}wifi.close();
            if (wake.isHeld()) wake.release();if (foreground) {stopForeground(STOP_FOREGROUND_REMOVE);foreground=false;}stopSelf();event("connection","disconnected");
        });
    }
    void scan() {
        stopScan();devices.clear();ensureStarted();
        try {
            BluetoothAdapter adapter=getSystemService(BluetoothManager.class).getAdapter();
            if (adapter==null || !adapter.isEnabled()) {event("error","请先开启系统蓝牙");return;}
            scanner=adapter.getBluetoothLeScanner();scanner.startScan(null,new ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(),scanCallback);
            event("phase","扫描 Pocket / DJI，持续 10 秒");main.postDelayed(scanTimeout,10000);
        } catch(Exception e){event("error","无法扫描：请授予附近设备权限");}
    }
    void stopScan() { main.removeCallbacks(scanTimeout);if(scanner!=null) {try {scanner.stopScan(scanCallback);}catch(Exception ignored){}scanner=null;} }
    private final ScanCallback scanCallback=new ScanCallback(){
        public void onScanResult(int type,ScanResult r) {
            try {
                String name=r.getDevice().getName();if(name==null && r.getScanRecord()!=null) name=r.getScanRecord().getDeviceName();
                boolean service=r.getScanRecord()!=null && r.getScanRecord().getServiceUuids()!=null && r.getScanRecord().getServiceUuids().contains(new ParcelUuid(GattBridge.SERVICE));
                String match=name==null?"":name.toLowerCase(Locale.ROOT);
                if(!service && !match.contains("pocket") && !match.contains("osmo") && !match.contains("dji")) return;
                String label=(name==null?"DJI BLE":name)+"  ·  "+r.getRssi()+" dBm";String mac=r.getDevice().getAddress();
                main.post(()->{devices.put(mac,label);event("devices",devices.size());});
            }catch(SecurityException ignored){}
        }
        public void onScanFailed(int code) {event("error","蓝牙扫描失败 "+code);}
    };
    void pair(String mac,String address,boolean skip) {if(connected||connecting){event("error","请先断开当前会话");return;}this.address=address;this.skipTCP=skip;stopScan();ensureStarted();if(gatt!=null)gatt.close();gatt=new GattBridge(this);event("phase","连接相机蓝牙");gatt.connect(mac);}
    void gattReady() {
        String id=getSharedPreferences("connection",0).getString("identifier",null);
        if(id==null)id=UUID.randomUUID().toString();
        id=id.replace("-","");getSharedPreferences("connection",0).edit().putString("identifier",id).apply();
        GattBridge current=gatt;
        call("ble-start",NativeBridge.object("identifier",id),result->{if(gatt==current && result.optBoolean("ok")){current.setLinkID(result.optJSONObject("data").optLong("link_id"));}});
    }
    private void handle(JSONObject event) {
        if(!alive)return;String kind=event.optString("kind");Object data=event.opt("data");
        if(kind.equals("gatt-operation")){if(gatt!=null)gatt.enqueue((JSONObject)data);else nativeFast("ble-ack",NativeBridge.object("id",((JSONObject)data).optLong("id"),"error","Android GATT 已关闭"));return;}
        if(kind.equals("credentials")){
            JSONObject credentials=(JSONObject)data;
            if(gatt==null || credentials.optLong("link_id")!=gatt.linkID)return;
            wifi.join(credentials.optString("ssid"),credentials.optString("password"));return;
        }
        if(kind.equals("ble-phase")){JSONObject value=(JSONObject)data;if(gatt==null||value.optLong("link_id")!=gatt.linkID)return;String p=value.optString("phase");data=p.equals("pairing")?"配对中":p.equals("awaiting-camera-approval")?"请在相机屏幕确认配对":p.equals("waking-ap")?"唤醒相机热点":"已获取相机热点信息";}
        if(kind.equals("ble-error")){JSONObject value=(JSONObject)data;if(gatt==null||value.optLong("link_id")!=gatt.linkID)return;data=value.optString("error");gatt.close();gatt=null;nativeFast("ble-stop",null);}
        event(kind,data);
    }
    void event(String kind,Object data) {
        if(Looper.myLooper()!=Looper.getMainLooper()){main.post(()->event(kind,data));return;}
        if(!alive)return;
        if(Arrays.asList("camera","battery","gimbal","storage","link").contains(kind))try{latestTelemetry.put(kind,data);}catch(JSONException ignored){}
        if(kind.equals("connection")) {
            connected="connected".equals(data);connecting="connecting".equals(data);
            phase=connected?"Pocket 3 已连接":connecting?"正在连接":"未连接";
            if(!connected&&!connecting)properties.clear();
            if(!connected&&!connecting){latestTelemetry.remove("camera");latestTelemetry.remove("battery");latestTelemetry.remove("gimbal");latestTelemetry.remove("storage");latestTelemetry.remove("link");}
            if(connected)wake.acquire(2*60*60*1000L);else if(!connecting&&wake.isHeld())wake.release();
        }
        if(kind.equals("phase")||kind.equals("wifi")||kind.equals("ble-phase"))phase=String.valueOf(data);
        if(kind.equals("script-state")){scriptPhase=String.valueOf(data);scriptRunning="running".equals(data);}
        if(kind.equals("property") && data instanceof JSONObject) {JSONObject value=(JSONObject)data;properties.put(value.optString("name"),value);}
        if(kind.equals("ble-error"))phase="蓝牙连接失败";
        if(Arrays.asList("error","ble-error","ble-trace","connection","phase","wifi","ble-phase","action","result","script-state").contains(kind)) {
            String stamp=new SimpleDateFormat("HH:mm:ss",Locale.ROOT).format(new Date());logs.addLast(stamp+"  "+kind+"  "+String.valueOf(data));while(logs.size()>180)logs.removeFirst();
            try(java.io.FileOutputStream out=openFileOutput("connection.log",MODE_PRIVATE)){out.write(String.join("\n",logs).getBytes(java.nio.charset.StandardCharsets.UTF_8));}catch(java.io.IOException ignored){}
        }
        for(Listener listener:new ArrayList<>(listeners))listener.onEvent(kind,data);
    }
    // Read-only, app-private snapshot for local hardware diagnostics; excludes Wi-Fi credentials.
    private final Runnable diagnosticSnapshot=new Runnable(){public void run(){
        if(!alive)return;
        String telemetry=latestTelemetry.toString();JSONArray reads=new JSONArray();for(JSONObject r:propertyReads)reads.put(r);String readResults=reads.toString();
        diagnostics.execute(()->{
            try{
                JSONObject snapshot=NativeBridge.object("timestamp_ms",System.currentTimeMillis(),"status",NativeBridge.call("status",null),"telemetry",new JSONObject(telemetry),"property_reads",new JSONArray(readResults));
                android.util.AtomicFile file=new android.util.AtomicFile(new java.io.File(getFilesDir(),"diagnostics.json"));java.io.FileOutputStream out=null;
                try{out=file.startWrite();out.write(snapshot.toString(2).getBytes(java.nio.charset.StandardCharsets.UTF_8));file.finishWrite(out);}catch(Exception e){file.failWrite(out);}
            }catch(Exception ignored){}finally{if(alive)main.postDelayed(this,1000);}
        });
    }};
    public void onTaskRemoved(Intent root) {disconnect();super.onTaskRemoved(root);}
    public void onDestroy() {
        alive=false;stopScan();NativeBridge.call("disconnect",null);
        if(gatt!=null)gatt.close();if(wifi!=null)wifi.close();if(wake!=null&&wake.isHeld())wake.release();
        workers.shutdownNow();fast.shutdownNow();polls.shutdownNow();diagnostics.shutdownNow();main.removeCallbacksAndMessages(null);listeners.clear();super.onDestroy();
    }
}
