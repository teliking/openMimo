// Package wirecatalog lists command identifiers recovered from native templates.
// Presence in this aggregate SDK catalog does not establish Pocket 3 support.
package wirecatalog

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed catalog.json
var data []byte

type Command struct {
	TemplateTag   uint8  `json:"template_tag"`
	CommandSet    uint8  `json:"command_set"`
	CommandID     uint16 `json:"command_id"`
	RequestType   string `json:"request_type"`
	ResponseType  string `json:"response_type"`
	NativeAddress string `json:"native_address"`
	NativeSymbol  string `json:"native_symbol"`
	Evidence      string `json:"evidence"`
	DeviceSupport string `json:"device_support"`
}
type Catalog struct {
	APKHash         string    `json:"apk_sha256"`
	Commands        []Command `json:"commands"`
	UnparsedSymbols []string  `json:"unparsed_symbols"`
}

func Load() (*Catalog, error) { var c Catalog; err := json.Unmarshal(data, &c); return &c, err }
func (c *Catalog) Find(query string) []Command {
	var found []Command
	for _, cmd := range c.Commands {
		if strings.Contains(strings.ToLower(cmd.RequestType), strings.ToLower(query)) {
			found = append(found, cmd)
		}
	}
	return found
}
