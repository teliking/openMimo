package dev.openmimo;

import android.app.*;
import android.content.Intent;
import android.net.*;
import android.os.Bundle;
import android.view.*;
import android.widget.*;
import org.json.*;

/** Debug APK's offline device checks. Never scans radios or sends camera actions. */
public final class SmokeInstrumentation extends Instrumentation {
    public void onCreate(Bundle args){super.onCreate(args);start();}
    private int assertions;
    private void check(boolean ok,String message){assertions++;if(!ok)throw new AssertionError(message);}
    private View find(View root,String value){
        if(root instanceof TextView && value.equals(((TextView)root).getText().toString()))return root;
        if(root instanceof ViewGroup){ViewGroup g=(ViewGroup)root;for(int i=0;i<g.getChildCount();i++){View found=find(g.getChildAt(i),value);if(found!=null)return found;}}
        return null;
    }
    public void onStart(){
        Bundle result=new Bundle();
        try{
            NativeBridge.call("disconnect",null);
            JSONObject self=NativeBridge.call("self-test",null);check(self.optBoolean("ok"),"JNI self-test failed: "+self);
            JSONObject checks=self.getJSONObject("data");check(checks.length()==6,"expected six native checks");for(java.util.Iterator<String> it=checks.keys();it.hasNext();)check(checks.getBoolean(it.next()),"native check failed");
            JSONObject invalid=NativeBridge.call("script-validate",NativeBridge.object("version",1,"name","invalid","repeat",1,"steps",new JSONArray().put(NativeBridge.object("action",NativeBridge.object("type","unknown")))));check(!invalid.optBoolean("ok"),"invalid script accepted");
            JSONObject unicode=NativeBridge.call("script-validate",NativeBridge.object("version",1,"name","拍摄 🎥","repeat",1,"steps",new JSONArray().put(NativeBridge.object("action",NativeBridge.object("type","photo")))));check(unicode.optBoolean("ok"),"UTF-8 JNI JSON failed");
            Activity activity=startActivitySync(new Intent(getTargetContext(),MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));waitForIdleSync();
            ConnectivityManager cm=getTargetContext().getSystemService(ConnectivityManager.class);Network wifi=null;for(Network network:cm.getAllNetworks()){NetworkCapabilities caps=cm.getNetworkCapabilities(network);if(caps!=null&&caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)){wifi=network;break;}}
            if(wifi!=null){JSONObject binding=NativeBridge.call("network-check",NativeBridge.object("handle",wifi.getNetworkHandle()));check(binding.optBoolean("ok"),"native Wi-Fi socket binding failed: "+binding);}
            runOnMainSync(()->{
                View root=activity.getWindow().getDecorView();check(find(root,"openMimo")!=null,"missing app title");
                String[] tabs={"拍摄","云台","任务","日志","连接"};String[] expected={"开始录像","云台归中","校验脚本","复制日志","运行离线自检"};
                for(int i=0;i<tabs.length;i++){View b=find(root,tabs[i]);check(b!=null,"missing tab "+tabs[i]);b.performClick();View control=find(root,expected[i]);check(control!=null,"missing control "+expected[i]);if(i<2)check(!control.isEnabled(),"camera action enabled while disconnected");}
            });
            runOnMainSync(()->getTargetContext().startForegroundService(new Intent(getTargetContext(),CameraService.class)));
            long deadline=android.os.SystemClock.elapsedRealtime()+3000;boolean foreground=false;
            while(android.os.SystemClock.elapsedRealtime()<deadline&&!foreground){for(ActivityManager.RunningServiceInfo info:getTargetContext().getSystemService(ActivityManager.class).getRunningServices(20))if(info.service.getClassName().equals(CameraService.class.getName())&&info.foreground)foreground=true;if(!foreground)android.os.SystemClock.sleep(50);}
            check(foreground,"connected-device foreground service did not start");
            runOnMainSync(()->getTargetContext().stopService(new Intent(getTargetContext(),CameraService.class)));
            result.putString("stream","openMimo offline JNI + UI + network-binding + service checks passed; assertions="+assertions+"; no camera commands sent\n");result.putInt("assertions",assertions);finish(Activity.RESULT_OK,result);
        }catch(Throwable error){result.putString("stream","FAILED: "+error.toString()+"\n");finish(Activity.RESULT_CANCELED,result);}
    }
}
