package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"openmimo/protocol/duml"
	"openmimo/protocol/pocket3"
	"openmimo/protocol/sdkvalue"
	"openmimo/protocol/wirecatalog"
	"openmimo/session"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func output(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mimo:", err)
		os.Exit(1)
	}
}
func usage() {
	fmt.Println(`mimo status
mimo catalog [--limit 20] [query]
mimo keys [--limit 20] [query]
mimo schema TYPE
mimo sdk-encode TYPE JSON_FILE (or - for stdin)
mimo sdk-decode TYPE HEX
mimo frame-decode HEX
mimo frame-encode --receiver 0xf0 --sequence 0x802b --set 0 --id 0x2b --flags 0x40 --payload 0400
mimo request --set 2 --id 0x8e --payload 00010f00 [--address 192.168.2.1:9004]
mimo video-format --resolution 16 --fps 6 --speed 0 [--sdk-codes]
mimo property-decode NAME HEX
mimo read-property --name cam_video_param_v2 [--timeout 5s]
mimo monitor [--address 192.168.2.1:9004] [--duration 10s] [--skip-tcp]

Catalogs cover the aggregate Mimo SDK; they are not Pocket 3 capability lists.
monitor requires an existing network connection to the camera AP.`)
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "status":
		c, err := sdkvalue.Load()
		if err != nil {
			return err
		}
		wire, err := wirecatalog.Load()
		if err != nil {
			return err
		}
		return output(map[string]any{"apk": "dji.mimo 2.12.1 / 10001997", "apk_sha256": c.SourceAPK, "target": "Pocket 3 / HG212", "sdk_layouts": len(c.Types), "sdk_enums": len(c.Enums), "sdk_keys": len(c.Keys), "native_command_templates": len(wire.Commands), "hardware_validation": "pending", "scope": "control protocol; BLE GATT adapters and media assembly pending"})
	case "catalog", "keys":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		limit := flags.Int("limit", 20, "maximum results; 0 means all")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *limit < 0 {
			return errors.New("negative limit")
		}
		query := strings.Join(flags.Args(), " ")
		if args[0] == "catalog" {
			c, err := wirecatalog.Load()
			if err != nil {
				return err
			}
			found := c.Find(query)
			total := len(found)
			if *limit > 0 && len(found) > *limit {
				found = found[:*limit]
			}
			return output(map[string]any{"device_support": "not-established-by-catalog", "total": total, "commands": found})
		}
		c, err := sdkvalue.Load()
		if err != nil {
			return err
		}
		found := c.FindKeys(query)
		total := len(found)
		if *limit > 0 && len(found) > *limit {
			found = found[:*limit]
		}
		return output(map[string]any{"layer": c.Layer, "total": total, "keys": found})
	case "schema":
		if len(args) != 2 {
			return errors.New("schema requires TYPE")
		}
		c, err := sdkvalue.Load()
		if err != nil {
			return err
		}
		name, err := c.Resolve(args[1])
		if err != nil {
			return err
		}
		return output(c.Types[name])
	case "sdk-encode":
		if len(args) != 3 {
			return errors.New("sdk-encode requires TYPE JSON_FILE")
		}
		var reader io.Reader = os.Stdin
		if args[2] != "-" {
			f, err := os.Open(args[2])
			if err != nil {
				return err
			}
			defer f.Close()
			reader = f
		}
		decoder := json.NewDecoder(io.LimitReader(reader, sdkvalue.MaxValueSize+1))
		decoder.UseNumber()
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return errors.New("input contains trailing or invalid JSON")
		}
		c, err := sdkvalue.Load()
		if err != nil {
			return err
		}
		data, err := c.Encode(args[1], value)
		if err != nil {
			return err
		}
		fmt.Println(hex.EncodeToString(data))
		return nil
	case "sdk-decode":
		if len(args) != 3 {
			return errors.New("sdk-decode requires TYPE HEX")
		}
		data, err := hex.DecodeString(args[2])
		if err != nil {
			return err
		}
		c, err := sdkvalue.Load()
		if err != nil {
			return err
		}
		value, err := c.Decode(args[1], data)
		if err != nil {
			return err
		}
		return output(value)
	case "frame-decode":
		if len(args) != 2 {
			return errors.New("frame-decode requires HEX")
		}
		data, err := hex.DecodeString(args[1])
		if err != nil {
			return err
		}
		frame, err := duml.Parse(data)
		if err != nil {
			return err
		}
		return output(map[string]any{"sender": frame.Sender, "receiver": frame.Receiver, "sequence": frame.Sequence, "flags": frame.Flags, "set": frame.CommandSet, "id": frame.CommandID, "payload_hex": hex.EncodeToString(frame.Payload)})
	case "frame-encode":
		return frameEncode(args[1:])
	case "video-format":
		return videoFormat(args[1:])
	case "read-property":
		return readProperty(ctx, args[1:])
	case "property-decode":
		if len(args) != 3 {
			return errors.New("property-decode requires NAME HEX")
		}
		data, err := hex.DecodeString(args[2])
		if err != nil {
			return err
		}
		return output(propertyValue(args[1], data))
	case "monitor":
		return monitor(ctx, args[1:])
	case "request":
		return request(ctx, args[1:])
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func frameEncode(args []string) error {
	flags := flag.NewFlagSet("frame-encode", flag.ContinueOnError)
	sender := flags.Uint("sender", 2, "encoded sender address")
	receiver := flags.Uint("receiver", 1, "encoded receiver address")
	sequence := flags.Uint("sequence", 0, "DUML sequence")
	set := flags.Uint("set", 2, "command set")
	id := flags.Uint("id", 0, "command id")
	control := flags.Uint("flags", 0x40, "DUML flags")
	payload := flags.String("payload", "", "payload hex")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected frame arguments")
	}
	if *sender > 255 || *receiver > 255 || *set > 255 || *id > 255 || *control > 255 || *sequence > 65535 {
		return errors.New("frame field outside wire range")
	}
	data, err := hex.DecodeString(*payload)
	if err != nil {
		return err
	}
	raw, err := (duml.Frame{Sender: byte(*sender), Receiver: byte(*receiver), Sequence: uint16(*sequence), Flags: byte(*control), CommandSet: byte(*set), CommandID: byte(*id), Payload: data}).MarshalBinary()
	if err != nil {
		return err
	}
	fmt.Println(hex.EncodeToString(raw))
	return nil
}
func monitor(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("monitor", flag.ContinueOnError)
	address := flags.String("address", "192.168.2.1:9004", "camera datalink address")
	duration := flags.Duration("duration", 10*time.Second, "monitor duration after connection; 0 means until interrupted")
	skipTCP := flags.Bool("skip-tcp", false, "skip TCP/7001 bootstrap")
	verbose := flags.Bool("verbose", false, "include unclassified frame metadata")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *duration < 0 {
		return errors.New("invalid monitor arguments")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := session.Dial(connectCtx, session.Config{Address: *address, SkipTCPBootstrap: *skipTCP})
	if err != nil {
		return err
	}
	defer client.Close()
	monitorCtx := ctx
	var stop context.CancelFunc = func() {}
	if *duration > 0 {
		monitorCtx, stop = context.WithTimeout(ctx, *duration)
	}
	defer stop()
	properties := pocket3.CoreProperties()
	for i, name := range properties {
		command, err := pocket3.SubscribeProperty(name, uint32(i+1))
		if err != nil {
			return err
		}
		if err = client.Send(monitorCtx, command); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	encoder := json.NewEncoder(os.Stdout)
	for {
		select {
		case <-monitorCtx.Done():
			return nil
		case <-ticker.C:
			s, err := client.Stats(monitorCtx)
			if err != nil {
				if monitorCtx.Err() != nil {
					return nil
				}
				return err
			}
			if err = encoder.Encode(map[string]any{"type": "link", "healthy": s.Healthy(time.Now()), "stats": s}); err != nil {
				return err
			}
		case event, open := <-client.Events():
			if !open {
				return client.Err()
			}
			if event.Frame == nil {
				continue
			}
			frame := event.Frame
			if frame.Encryption() != 0 {
				if *verbose {
					if err = encoder.Encode(map[string]any{"type": "encrypted-frame", "set": frame.CommandSet, "id": frame.CommandID, "payload_length": len(frame.Payload)}); err != nil {
						return err
					}
				}
				continue
			}
			var kind string
			var value any
			var parseErr error
			switch {
			case frame.CommandSet == 4 && frame.CommandID == 5:
				kind = "gimbal"
				value, parseErr = pocket3.ParseGimbal(frame.Payload)
			case frame.CommandSet == 2 && frame.CommandID == 0x80:
				kind = "camera"
				value, parseErr = pocket3.ParseCameraState(frame.Payload)
			case frame.CommandSet == 0x0d && frame.CommandID == 2:
				kind = "battery"
				value, parseErr = pocket3.ParseBattery(frame.Payload)
			case frame.CommandSet == 2 && frame.CommandID == 0xdc:
				kind = "storage"
				value, parseErr = pocket3.ParseStorage(frame.Payload)
			case frame.CommandSet == 0 && frame.CommandID == 0x99:
				kind = "property"
				p, err := pocket3.ParsePropertyPush(frame.Payload)
				parseErr = err
				v := propertyValue(p.Name, p.Value)
				v["transaction"] = p.Transaction
				value = v
			default:
				if !*verbose {
					continue
				}
				kind = "frame"
				value = map[string]any{"sender": frame.Sender, "receiver": frame.Receiver, "sequence": frame.Sequence, "set": frame.CommandSet, "id": frame.CommandID, "flags": frame.Flags, "payload_length": len(frame.Payload)}
			}
			if parseErr != nil {
				if *verbose {
					if err = encoder.Encode(map[string]any{"type": "parse-error", "set": frame.CommandSet, "id": frame.CommandID, "error": parseErr.Error()}); err != nil {
						return err
					}
				}
				continue
			}
			if err = encoder.Encode(map[string]any{"type": kind, "value": value}); err != nil {
				return err
			}
		}
	}
}
