package sdkvalue

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOriginalJavaSerializerVectors(t *testing.T) {
	data, err := os.ReadFile("../testdata/mimo-java-values.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Vectors []struct {
			Name  string         `json:"name"`
			Type  string         `json:"type"`
			Value map[string]any `json:"value"`
			Hex   string         `json:"hex"`
		} `json:"vectors"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err = dec.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range fixtures.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			got, err := c.Encode(v.Type, v.Value)
			if err != nil {
				t.Fatal(err)
			}
			want, err := hex.DecodeString(v.Hex)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Go %x != original Java %x", got, want)
			}
			decoded, err := c.Decode(v.Type, want)
			if err != nil {
				t.Fatal(err)
			}
			roundtrip, err := c.Encode(v.Type, decoded)
			if err != nil || !bytes.Equal(roundtrip, want) {
				t.Fatalf("decode/re-encode mismatch: %v", err)
			}
		})
	}
}
func TestCatalogReferencesResolve(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unsupported) != 0 {
		t.Fatalf("%d unresolved layouts", len(c.Unsupported))
	}
	primitive := map[string]bool{"bool": true, "int32": true, "int64": true, "float64": true, "string": true, "bytes": true}
	for name, layout := range c.Types {
		for _, field := range layout.Fields {
			typ := strings.TrimPrefix(field.Type, "[]")
			if !primitive[typ] {
				if _, ok := c.Types[typ]; !ok {
					t.Fatalf("%s.%s unresolved %s", name, field.Name, typ)
				}
			}
			if field.Enum != "" {
				if _, ok := c.Enums[field.Enum]; !ok {
					t.Fatalf("unknown enum %s", field.Enum)
				}
			}
		}
	}
}
func TestMalformedValues(t *testing.T) {
	c, _ := Load()
	if _, err := c.Decode("CameraWorkModeRangeMsg", []byte{0xff, 0xff, 0xff, 0x7f}); err == nil {
		t.Fatal("unbounded list accepted")
	}
	if _, err := c.Encode("VideoResolutionFrameRate", map[string]any{"resolution": 1}); err == nil {
		t.Fatal("missing field accepted")
	}
	if _, err := c.Encode("VideoResolutionFrameRate", map[string]any{"resolution": int64(1) << 40, "frameRate": 1}); err == nil {
		t.Fatal("int32 overflow accepted")
	}
	if _, err := c.Decode("VideoResolutionFrameRate", make([]byte, 9)); err == nil {
		t.Fatal("trailing byte accepted")
	}
	if _, err := c.Decode("VideoResolutionFrameRate", make([]byte, 7)); err == nil {
		t.Fatal("truncated value accepted")
	}
}

func TestEncodeDecodeShareAggregateListBudget(t *testing.T) {
	c := &Catalog{Types: map[string]Layout{"Test": {Fields: []Field{{Name: "a", Type: "[]int32"}, {Name: "b", Type: "[]int32"}}}}}
	value := map[string]any{"a": make([]int32, MaxListItems/2), "b": make([]int32, MaxListItems/2)}
	encoded, err := c.Encode("Test", value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Decode("Test", encoded); err != nil {
		t.Fatal(err)
	}
	value["b"] = make([]int32, MaxListItems/2+1)
	if _, err = c.Encode("Test", value); err == nil {
		t.Fatal("aggregate encoder item budget exceeded")
	}
}
