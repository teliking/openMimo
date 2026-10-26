//go:build android

package main

/*
#include <jni.h>
#include <android/multinetwork.h>
#cgo LDFLAGS: -landroid
static inline int bind_network(unsigned long long handle, int fd) { return android_setsocknetwork((net_handle_t)handle, fd); }
static inline int array_length(JNIEnv *env, jbyteArray a) { return (*env)->GetArrayLength(env, a); }
static inline void read_array(JNIEnv *env, jbyteArray a, jbyte *out, int n) { (*env)->GetByteArrayRegion(env, a, 0, n, out); }
static inline jbyteArray new_array(JNIEnv *env, jbyte *data, int n) {
    jbyteArray a = (*env)->NewByteArray(env, n);
    if (a != NULL && n > 0) (*env)->SetByteArrayRegion(env, a, 0, n, data);
    return a;
}
*/
import "C"
import (
	"fmt"
	"openmimo/mobilecore"
	"syscall"
	"unsafe"
)

var engine = mobilecore.New()

func init() {
	engine.SetSocketBinder(func(handle uint64, raw syscall.RawConn) error {
		if handle == 0 {
			return nil
		}
		var bindErr error
		err := raw.Control(func(fd uintptr) {
			result, callErr := C.bind_network(C.ulonglong(handle), C.int(fd))
			if result != 0 {
				bindErr = fmt.Errorf("Android network bind: %w", callErr)
			}
		})
		if err != nil {
			return err
		}
		return bindErr
	})
}

//export Java_dev_openmimo_NativeBridge_dispatch
func Java_dev_openmimo_NativeBridge_dispatch(env *C.JNIEnv, clazz C.jclass, input C.jbyteArray) C.jbyteArray {
	n := int(C.array_length(env, input))
	var data []byte
	if n >= 0 && n <= 1<<20 {
		data = make([]byte, n)
		if n > 0 {
			C.read_array(env, input, (*C.jbyte)(unsafe.Pointer(&data[0])), C.int(n))
		}
	} else {
		data = []byte(`{"op":"invalid-oversized-request"}`)
	}
	out := engine.Dispatch(data)
	return C.new_array(env, (*C.jbyte)(unsafe.Pointer(&out[0])), C.int(len(out)))
}
func main() {}
