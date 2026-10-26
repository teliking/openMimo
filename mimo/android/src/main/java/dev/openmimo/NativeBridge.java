package dev.openmimo;

import java.nio.charset.StandardCharsets;
import org.json.JSONObject;

final class NativeBridge {
    static { System.loadLibrary("mimo"); }
    private static native byte[] dispatch(byte[] request);
    static JSONObject call(String op, Object params) {
        try {
            JSONObject request = new JSONObject().put("op", op);
            if (params != null) request.put("params", params);
            byte[] result = dispatch(request.toString().getBytes(StandardCharsets.UTF_8));
            return new JSONObject(new String(result, StandardCharsets.UTF_8));
        } catch (Exception error) {
            try { return new JSONObject().put("ok", false).put("error", error.toString()); }
            catch (Exception impossible) { throw new IllegalStateException(impossible); }
        }
    }
    static JSONObject object(Object... pairs) {
        JSONObject value = new JSONObject();
        try { for (int i = 0; i < pairs.length; i += 2) value.put((String)pairs[i], pairs[i+1]); }
        catch (Exception error) { throw new IllegalArgumentException(error); }
        return value;
    }
}
