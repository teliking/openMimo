package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"openmimo/protocol/pocket3"
	"openmimo/session"
	"time"
)

func propertyValue(name string, data []byte) map[string]any {
	v := map[string]any{"name": name, "value_hex": hex.EncodeToString(data)}
	decoded, err := pocket3.DecodeProperty(name, data)
	if err == nil {
		v["decoded"] = decoded
	} else if !errors.Is(err, pocket3.ErrUnknownProperty) {
		v["decode_error"] = err.Error()
	}
	return v
}

func readProperty(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("read-property", flag.ContinueOnError)
	name := flags.String("name", "cam_video_param_v2", "camera property name")
	address := flags.String("address", "192.168.2.1:9004", "camera address")
	skipTCP := flags.Bool("skip-tcp", false, "skip TCP bootstrap")
	timeout := flags.Duration("timeout", 5*time.Second, "property response timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 {
		return errors.New("invalid property arguments")
	}
	if _, err := pocket3.SubscribeProperty(*name, 1); err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := session.Dial(connectCtx, session.Config{Address: *address, SkipTCPBootstrap: *skipTCP, RequestTimeout: *timeout})
	if err != nil {
		return err
	}
	defer c.Close()
	p, err := c.ReadProperty(ctx, *name)
	if err != nil {
		return err
	}
	v := propertyValue(p.Name, p.Value)
	v["transaction"] = p.Transaction
	return output(v)
}

func videoFormat(args []string) error {
	flags := flag.NewFlagSet("video-format", flag.ContinueOnError)
	resolution := flags.Uint("resolution", 16, "resolution code (default 4K)")
	fps := flags.Uint("fps", 6, "frame-rate code (default 60fps)")
	speed := flags.Uint("speed", 0, "raw speed ratio, six bits")
	sdk := flags.Bool("sdk-codes", false, "convert SDK codes to wire codes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *resolution > 255 || *fps > 255 || *speed > 63 {
		return errors.New("video fields outside supported wire range")
	}
	res, rate := byte(*resolution), byte(*fps)
	if *sdk {
		res = pocket3.SDKResolutionToWire(uint32(*resolution))
		rate = pocket3.SDKFrameRateToWire(uint32(*fps))
	}
	command, err := (pocket3.VideoSettings{Resolution: res, FrameRate: rate, SpeedRatio: byte(*speed)}).Command()
	if err != nil {
		return err
	}
	return output(map[string]any{"receiver": command.Receiver, "set": command.Set, "id": command.ID, "flags": command.Flags, "payload_hex": hex.EncodeToString(command.Payload), "hardware_validation": "pending"})
}
