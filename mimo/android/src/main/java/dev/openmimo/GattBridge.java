package dev.openmimo;

import android.bluetooth.*;
import android.os.*;
import java.util.*;
import org.json.JSONObject;

/** Serializes Android GATT operations and acknowledges Go only after callbacks. */
final class GattBridge {
    static final UUID SERVICE = UUID.fromString("0000fff0-0000-1000-8000-00805f9b34fb");
    private static final UUID CCCD = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb");
    private final CameraService owner;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final ArrayDeque<JSONObject> queue = new ArrayDeque<>();
    private BluetoothGatt gatt;
    private JSONObject active;
    private boolean closed;
    long linkID;
    GattBridge(CameraService owner) { this.owner = owner; }
    void connect(String address) {
        try {
            BluetoothManager manager = owner.getSystemService(BluetoothManager.class);
            if (manager == null || manager.getAdapter() == null || !manager.getAdapter().isEnabled()) throw new IllegalStateException("请先开启蓝牙");
            gatt = manager.getAdapter().getRemoteDevice(address).connectGatt(owner, false, callbacks, BluetoothDevice.TRANSPORT_LE);
            main.postDelayed(() -> { if (!closed && gatt != null && !ready) fail("蓝牙连接或服务发现超时"); }, 15000);
        } catch (Exception error) { fail(error.getMessage()); }
    }
    private boolean ready;
    private boolean discoveryStarted;
    private int mtu=23;
    private void discover(BluetoothGatt g){
        if(closed||discoveryStarted)return;discoveryStarted=true;
        if(!g.discoverServices())fail("无法发现蓝牙服务");
    }
    void enqueue(JSONObject operation) {
        if (closed) { ack(operation, "GATT 已关闭"); return; }
        if (queue.size() >= 64) { ack(operation, "GATT 操作队列已满"); return; }
        queue.add(operation); next();
    }
    private void next() {
        if (closed || active != null || queue.isEmpty() || linkID == 0) return;
        active = queue.remove();
        if(active.optLong("link_id")!=linkID){JSONObject stale=active;active=null;ack(stale,"过期 GATT 操作");next();return;}
        try {
            if (!ready || gatt == null) throw new IllegalStateException("GATT 尚未就绪");
            BluetoothGattService service = gatt.getService(SERVICE);
            BluetoothGattCharacteristic c = service.getCharacteristic(UUID.fromString(active.getString("uuid")));
            if (c == null) throw new IllegalStateException("相机缺少所需蓝牙特征");
            if (active.getString("operation").equals("subscribe")) {
                if (!gatt.setCharacteristicNotification(c, true)) throw new IllegalStateException("无法启用通知");
                BluetoothGattDescriptor descriptor = c.getDescriptor(CCCD);
                if (descriptor == null) throw new IllegalStateException("蓝牙特征缺少 CCCD");
                byte[] value = (c.getProperties() & BluetoothGattCharacteristic.PROPERTY_NOTIFY) != 0
                    ? BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE : BluetoothGattDescriptor.ENABLE_INDICATION_VALUE;
                if (Build.VERSION.SDK_INT >= 33) {
                    if (gatt.writeDescriptor(descriptor, value) != BluetoothStatusCodes.SUCCESS) throw new IllegalStateException("通知订阅未入队");
                } else {
                    descriptor.setValue(value); if (!gatt.writeDescriptor(descriptor)) throw new IllegalStateException("通知订阅未入队");
                }
            } else {
                byte[] value = unhex(active.getString("data_hex"));
                int type = active.optBoolean("without_response") ? BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE : BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT;
                if (Build.VERSION.SDK_INT >= 33) {
                    if (gatt.writeCharacteristic(c, value, type) != BluetoothStatusCodes.SUCCESS) throw new IllegalStateException("蓝牙写入未入队");
                } else {
                    c.setWriteType(type); c.setValue(value); if (!gatt.writeCharacteristic(c)) throw new IllegalStateException("蓝牙写入未入队");
                }
            }
            long id = active.getLong("id");
            main.postDelayed(() -> { if (active != null && active.optLong("id") == id) fail("GATT 操作超时"); }, 5000);
        } catch (Exception error) { complete(error.getMessage()); }
    }
    private void complete(String error) {
        if (active == null) return;
        JSONObject done = active; active = null; ack(done, error == null ? "" : error);
        if (error != null && !error.isEmpty()) { fail(error); return; }
        next();
    }
    private void ack(JSONObject op, String error) { owner.nativeFast("ble-ack", NativeBridge.object("id", op.optLong("id"), "error", error)); }
    private void notifyValue(BluetoothGattCharacteristic c, byte[] value) {
        if (!closed) owner.nativeFast("ble-notify", NativeBridge.object("link_id",linkID,"uuid", c.getUuid().toString(), "data_hex", hex(value), "error", ""));
    }
    private void fail(String message) {
        if (closed) return;
        owner.event("ble-error", message == null ? "蓝牙失败" : message);
        owner.nativeFast("ble-notify", NativeBridge.object("link_id",linkID,"uuid", "", "data_hex", "", "error", message == null ? "GATT failed" : message));
        close();
    }
    void close() {
        if (closed) return; closed = true;
        if (active != null) { ack(active, "GATT 已关闭"); active = null; }
        while (!queue.isEmpty()) ack(queue.remove(), "GATT 已关闭");
        if (gatt != null) { try { gatt.disconnect(); gatt.close(); } catch (Exception ignored) {} gatt = null; }
    }
    void setLinkID(long id){if(!closed){linkID=id;next();}}
    private final BluetoothGattCallback callbacks = new BluetoothGattCallback() {
        public void onConnectionStateChange(BluetoothGatt g, int status, int state) {
            main.post(() -> {
                if (closed) return;
                if (status != BluetoothGatt.GATT_SUCCESS || state == BluetoothProfile.STATE_DISCONNECTED) { fail("GATT 连接已断开，状态 " + status); return; }
                if (state == BluetoothProfile.STATE_CONNECTED) {
                    if(!g.requestMtu(517))discover(g);
                    else main.postDelayed(()->discover(g),3000);
                }
            });
        }
        public void onMtuChanged(BluetoothGatt g,int value,int status){
            main.post(()->{if(closed)return;if(status==BluetoothGatt.GATT_SUCCESS)mtu=value;owner.event("ble-trace","协商 ATT MTU="+mtu);discover(g);});
        }
        public void onServicesDiscovered(BluetoothGatt g, int status) {
            main.post(() -> {
                if (closed) return;
                if (status != BluetoothGatt.GATT_SUCCESS || g.getService(SERVICE) == null) { fail("未发现 Pocket 3 蓝牙服务"); return; }
                ready = true; owner.gattReady();
            });
        }
        public void onDescriptorWrite(BluetoothGatt g, BluetoothGattDescriptor d, int status) {
            main.post(() -> complete(status == BluetoothGatt.GATT_SUCCESS ? null : "GATT 订阅失败 " + status));
        }
        public void onCharacteristicWrite(BluetoothGatt g, BluetoothGattCharacteristic c, int status) {
            main.post(() -> complete(status == BluetoothGatt.GATT_SUCCESS ? null : "GATT 写入失败 " + status));
        }
        public void onCharacteristicChanged(BluetoothGatt g, BluetoothGattCharacteristic c, byte[] value) { notifyValue(c, value.clone()); }
        @SuppressWarnings("deprecation") public void onCharacteristicChanged(BluetoothGatt g, BluetoothGattCharacteristic c) {
            if (Build.VERSION.SDK_INT < 33) notifyValue(c, c.getValue().clone());
        }
    };
    static String hex(byte[] data) { StringBuilder s = new StringBuilder(); for (byte b : data) s.append(String.format(Locale.ROOT, "%02x", b & 255)); return s.toString(); }
    private static byte[] unhex(String s) {
        if ((s.length() & 1) != 0) throw new IllegalArgumentException("invalid hex");
        byte[] b = new byte[s.length()/2]; for (int i = 0; i < b.length; i++) b[i] = (byte)Integer.parseInt(s.substring(i*2, i*2+2), 16); return b;
    }
}
