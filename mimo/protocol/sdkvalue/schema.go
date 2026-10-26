// Package sdkvalue implements the DJI Java/JNI value ABI recovered from Mimo.
// Its bytes are SDK values. They are not camera transport packets.
package sdkvalue

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed schema.json
var schemaJSON []byte

type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Enum string `json:"enum,omitempty"`
}

type Layout struct {
	Fields       []Field `json:"fields"`
	Source       string  `json:"source"`
	SourceSHA256 string  `json:"source_sha256"`
}

type Key struct {
	Class      string `json:"class"`
	Field      string `json:"field"`
	Identifier string `json:"identifier"`
	JavaType   string `json:"java_type"`
	CanGet     bool   `json:"can_get"`
	CanSet     bool   `json:"can_set"`
	CanListen  bool   `json:"can_listen"`
	CanAction  bool   `json:"can_action"`
	Event      bool   `json:"event"`
	Source     string `json:"source"`
}

type Catalog struct {
	SourceAPK   string                      `json:"source_apk_sha256"`
	Layer       string                      `json:"layer"`
	Types       map[string]Layout           `json:"types"`
	Enums       map[string]map[string]int64 `json:"enums"`
	Unsupported map[string]string           `json:"unsupported"`
	Keys        []Key                       `json:"keys"`
}

// Load returns a private catalog; callers can edit it without changing other users.
func Load() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(schemaJSON, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) Resolve(name string) (string, error) {
	if _, ok := c.Types[name]; ok {
		return name, nil
	}
	var found string
	for qualified := range c.Types {
		if strings.HasSuffix(qualified, "."+name) {
			if found != "" {
				return "", fmt.Errorf("ambiguous type %q", name)
			}
			found = qualified
		}
	}
	if found == "" {
		return "", fmt.Errorf("unknown or unsupported SDK type %q", name)
	}
	return found, nil
}

func (c *Catalog) TypeNames() []string {
	names := make([]string, 0, len(c.Types))
	for name := range c.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FindKeys returns aggregate SDK declarations, not per-device capabilities.
func (c *Catalog) FindKeys(query string) []Key {
	var result []Key
	for _, key := range c.Keys {
		if strings.Contains(strings.ToLower(key.Class+"."+key.Identifier), strings.ToLower(query)) {
			result = append(result, key)
		}
	}
	return result
}
