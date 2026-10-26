package dev.openmimo;

import android.net.*;
import android.net.wifi.WifiNetworkSpecifier;
import android.os.*;

/** Owns only this app's Wi-Fi request. Go sockets get an explicit network handle. */
final class WifiLink {
    private final CameraService owner;
    private final ConnectivityManager manager;
    private ConnectivityManager.NetworkCallback callback;
    private Network selected;
    private long generation;
    WifiLink(CameraService owner) { this.owner = owner; manager = owner.getSystemService(ConnectivityManager.class); }
    void join(String ssid, String password) {
        close(); final long token = ++generation;
        try {
            WifiNetworkSpecifier.Builder spec = new WifiNetworkSpecifier.Builder().setSsid(ssid);
            if (!password.isEmpty()) spec.setWpa2Passphrase(password);
            NetworkRequest request = new NetworkRequest.Builder().addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
                .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).setNetworkSpecifier(spec.build()).build();
            callback = new ConnectivityManager.NetworkCallback() {
                public void onAvailable(Network network) { owner.main.post(() -> { if (token == generation) select(network); }); }
                public void onUnavailable() { owner.main.post(() -> { if (token == generation) owner.event("error", "未能加入相机 Wi-Fi，可在系统设置中连接后重试"); }); }
                public void onLost(Network network) { owner.main.post(() -> { if (token == generation && network.equals(selected)) { selected = null; owner.networkLost(); } }); }
            };
            manager.requestNetwork(request, callback, 30000);
            owner.event("wifi", "等待系统连接相机 Wi-Fi");
        } catch (Exception error) { owner.event("error", "Wi-Fi 请求失败：" + error.getClass().getSimpleName()); }
    }
    boolean useCurrent() {
        close();final long token=++generation;
        Network best=null;int bestPrefix=-1;java.net.InetAddress target=null;
        try{String host=owner.address.startsWith("[")?owner.address.substring(1,owner.address.indexOf(']')):owner.address.substring(0,owner.address.lastIndexOf(':'));target=InetAddresses.parseNumericAddress(host);}catch(Exception ignored){}
        for (Network network : manager.getAllNetworks()) {
            NetworkCapabilities caps = manager.getNetworkCapabilities(network);
            if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                int prefix=0;LinkProperties properties=manager.getLinkProperties(network);if(target!=null&&properties!=null)for(RouteInfo route:properties.getRoutes())if(route.matches(target))prefix=Math.max(prefix,route.getDestination().getPrefixLength());
                if(prefix>bestPrefix){best=network;bestPrefix=prefix;}
            }
        }
        if(best!=null){callback=new ConnectivityManager.NetworkCallback(){public void onLost(Network lost){owner.main.post(()->{if(token==generation&&lost.equals(selected)){selected=null;owner.networkLost();}});}};manager.registerNetworkCallback(new NetworkRequest.Builder().addTransportType(NetworkCapabilities.TRANSPORT_WIFI).removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).build(),callback);select(best);return true;}
        owner.event("error", "未发现 Wi-Fi 网络，请先连接相机热点"); return false;
    }
    private void select(Network network) {
        selected = network;
        JSONObjectSafeNetwork(network.getNetworkHandle());
        owner.event("wifi", "已选定相机 Wi-Fi，正在建立控制会话");
        owner.connectControl();
    }
    private void JSONObjectSafeNetwork(long handle) { NativeBridge.call("network", NativeBridge.object("handle", handle)); }
    void close() {
        generation++; selected = null;
        if (callback != null) { try { manager.unregisterNetworkCallback(callback); } catch (Exception ignored) {} callback = null; }
        NativeBridge.call("network", NativeBridge.object("handle", 0));
    }
}
