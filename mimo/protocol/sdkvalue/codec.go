package sdkvalue

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxValueSize = 16 << 20
const MaxListItems = 65536
const maxDepth = 64

func integer(value any) (int64, error) {
	switch v := value.(type) {
	case json.Number:
		return v.Int64()
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v <= math.MaxInt64 {
			return int64(v), nil
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v && v >= -9223372036854775808.0 && v < 9223372036854775808.0 {
			return int64(v), nil
		}
	}
	return 0, fmt.Errorf("expected signed integer, got %T (%v)", value, value)
}

// Encode requires every field explicitly, so defaults never invent firmware behavior.
func (c *Catalog) Encode(name string, value map[string]any) ([]byte, error) {
	qualified, err := c.Resolve(name)
	if err != nil {
		return nil, err
	}
	budget := MaxListItems
	result, err := c.encodeType(nil, qualified, value, "", 0, &budget)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func append32(out []byte, value uint32) []byte { return binary.LittleEndian.AppendUint32(out, value) }

func (c *Catalog) encodeType(out []byte, typ string, value any, enum string, depth int, budget *int) ([]byte, error) {
	if depth > maxDepth {
		return nil, errors.New("SDK value nesting exceeds limit")
	}
	if len(out) > MaxValueSize {
		return nil, errors.New("SDK value exceeds size limit")
	}
	if strings.HasPrefix(typ, "[]") {
		v := reflect.ValueOf(value)
		if !v.IsValid() || (v.Kind() != reflect.Array && v.Kind() != reflect.Slice) {
			return nil, fmt.Errorf("%s: expected list", typ)
		}
		if v.Len() > *budget {
			return nil, errors.New("SDK list exceeds item limit")
		}
		*budget -= v.Len()
		out = append32(out, uint32(v.Len()))
		for i := 0; i < v.Len(); i++ {
			var err error
			out, err = c.encodeType(out, typ[2:], v.Index(i).Interface(), enum, depth+1, budget)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
		}
		return out, nil
	}
	if enum != "" {
		if label, ok := value.(string); ok {
			values, exists := c.Enums[enum]
			if !exists {
				return nil, fmt.Errorf("enum schema unavailable: %s", enum)
			}
			number, exists := values[label]
			if !exists {
				return nil, fmt.Errorf("%s: unknown enum label %q", enum, label)
			}
			value = number
		}
	}
	switch typ {
	case "bool":
		v, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", value)
		}
		if v {
			return append(out, 1), nil
		}
		return append(out, 0), nil
	case "int32":
		v, err := integer(value)
		if err != nil {
			return nil, err
		}
		if v < math.MinInt32 || v > math.MaxInt32 {
			return nil, errors.New("int32 out of range")
		}
		return append32(out, uint32(v)), nil
	case "int64":
		v, err := integer(value)
		if err != nil {
			return nil, err
		}
		return binary.LittleEndian.AppendUint64(out, uint64(v)), nil
	case "float64":
		var v float64
		switch n := value.(type) {
		case float64:
			v = n
		case json.Number:
			var err error
			v, err = n.Float64()
			if err != nil {
				return nil, err
			}
		default:
			i, err := integer(value)
			if err != nil {
				return nil, err
			}
			v = float64(i)
		}
		return binary.LittleEndian.AppendUint64(out, math.Float64bits(v)), nil
	case "string":
		s, ok := value.(string)
		if !ok || !utf8.ValidString(s) {
			return nil, errors.New("expected valid UTF-8 string")
		}
		if len(s) > MaxValueSize-len(out)-4 {
			return nil, errors.New("SDK string exceeds size limit")
		}
		out = append32(out, uint32(len(s)))
		return append(out, s...), nil
	case "bytes":
		b, ok := value.([]byte)
		if encoded, isString := value.(string); isString {
			var err error
			b, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, fmt.Errorf("invalid base64 bytes value: %w", err)
			}
			ok = true
		}
		if !ok {
			return nil, errors.New("bytes value requires []byte")
		}
		if len(b) > MaxValueSize-len(out)-4 {
			return nil, errors.New("SDK bytes exceed size limit")
		}
		out = append32(out, uint32(len(b)))
		return append(out, b...), nil
	}
	layout, ok := c.Types[typ]
	if !ok {
		return nil, fmt.Errorf("unavailable nested layout %s", typ)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected object", typ)
	}
	allowed := make(map[string]bool, len(layout.Fields))
	for _, field := range layout.Fields {
		allowed[field.Name] = true
		v, exists := object[field.Name]
		if !exists {
			return nil, fmt.Errorf("%s.%s: required field missing", typ, field.Name)
		}
		var err error
		out, err = c.encodeType(out, field.Type, v, field.Enum, depth+1, budget)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", typ, field.Name, err)
		}
	}
	for field := range object {
		if !allowed[field] {
			return nil, fmt.Errorf("%s: unknown field %q", typ, field)
		}
	}
	if len(out) > MaxValueSize {
		return nil, errors.New("SDK value exceeds size limit")
	}
	return out, nil
}

type decoder struct {
	data   []byte
	offset int
	budget int
}

func (d *decoder) take(size int) ([]byte, error) {
	if size < 0 || size > len(d.data)-d.offset {
		return nil, fmt.Errorf("truncated SDK value at offset %d: need %d bytes", d.offset, size)
	}
	result := d.data[d.offset : d.offset+size]
	d.offset += size
	return result, nil
}
func (d *decoder) count() (int, error) {
	b, err := d.take(4)
	if err != nil {
		return 0, err
	}
	n := uint64(binary.LittleEndian.Uint32(b))
	if n > MaxValueSize {
		return 0, errors.New("SDK length exceeds limit")
	}
	return int(n), nil
}

// Decode rejects trailing bytes to catch a wrong type or a changed SDK layout.
// Numeric enum values are preserved, including values absent from this APK.
func (c *Catalog) Decode(name string, data []byte) (map[string]any, error) {
	if len(data) > MaxValueSize {
		return nil, errors.New("SDK value exceeds size limit")
	}
	qualified, err := c.Resolve(name)
	if err != nil {
		return nil, err
	}
	d := decoder{data: data, budget: MaxListItems}
	value, err := c.decodeType(&d, qualified, 0)
	if err != nil {
		return nil, err
	}
	if d.offset != len(data) {
		return nil, fmt.Errorf("%d trailing SDK bytes", len(data)-d.offset)
	}
	return value.(map[string]any), nil
}

func (c *Catalog) decodeType(d *decoder, typ string, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("SDK value nesting exceeds limit")
	}
	if strings.HasPrefix(typ, "[]") {
		n, err := d.count()
		if err != nil {
			return nil, err
		}
		if n > d.budget {
			return nil, errors.New("SDK list allocation budget exceeded")
		}
		d.budget -= n
		result := make([]any, n)
		for i := range result {
			result[i], err = c.decodeType(d, typ[2:], depth+1)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
		}
		return result, nil
	}
	size := 0
	switch typ {
	case "bool":
		size = 1
	case "int32":
		size = 4
	case "int64", "float64":
		size = 8
	}
	if size != 0 {
		b, err := d.take(size)
		if err != nil {
			return nil, err
		}
		switch typ {
		case "bool":
			return b[0] != 0, nil
		case "int32":
			return int32(binary.LittleEndian.Uint32(b)), nil
		case "int64":
			return int64(binary.LittleEndian.Uint64(b)), nil
		case "float64":
			return math.Float64frombits(binary.LittleEndian.Uint64(b)), nil
		}
	}
	if typ == "string" || typ == "bytes" {
		n, err := d.count()
		if err != nil {
			return nil, err
		}
		b, err := d.take(n)
		if err != nil {
			return nil, err
		}
		if typ == "bytes" {
			return append([]byte(nil), b...), nil
		}
		if !utf8.Valid(b) {
			return nil, errors.New("invalid UTF-8 SDK string")
		}
		return string(b), nil
	}
	layout, ok := c.Types[typ]
	if !ok {
		return nil, fmt.Errorf("unavailable nested layout %s", typ)
	}
	result := make(map[string]any, len(layout.Fields))
	for _, field := range layout.Fields {
		value, err := c.decodeType(d, field.Type, depth+1)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", typ, field.Name, err)
		}
		result[field.Name] = value
	}
	return result, nil
}

// EnumName provides a presentation label without discarding unknown numbers.
func (c *Catalog) EnumName(enum string, value int64) string {
	for name, number := range c.Enums[enum] {
		if number == value {
			return name
		}
	}
	return strconv.FormatInt(value, 10)
}
