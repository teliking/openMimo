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

// request sends exactly one caller-selected command after establishing a session.
// It reports the raw response; status-byte semantics vary between commands.
func request(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("request", flag.ContinueOnError)
	address := flags.String("address", "192.168.2.1:9004", "camera datalink address")
	skipTCP := flags.Bool("skip-tcp", false, "skip TCP/7001 bootstrap")
	timeout := flags.Duration("timeout", 2*time.Second, "response timeout after connection")
	receiver := flags.Uint("receiver", 1, "encoded receiver address")
	set := flags.Uint("set", 0, "command set (required)")
	id := flags.Uint("id", 0, "command ID (required)")
	control := flags.Uint("flags", 0x40, "unencrypted DUML control flags")
	payload := flags.String("payload", "", "payload hex")
	if err := flags.Parse(args); err != nil {
		return err
	}
	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if !provided["set"] || !provided["id"] || flags.NArg() != 0 || *timeout <= 0 {
		return errors.New("request requires --set, --id and a positive timeout")
	}
	if *receiver > 255 || *set > 255 || *id > 255 || *control > 255 || *control&7 != 0 {
		return errors.New("request fields outside supported wire range")
	}
	data, err := hex.DecodeString(*payload)
	if err != nil {
		return err
	}
	command := pocket3.Command{Name: "raw-request", Receiver: byte(*receiver), Set: byte(*set), ID: byte(*id), Flags: byte(*control), Payload: data}
	if _, err = command.Frame(0).MarshalBinary(); err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := session.Dial(connectCtx, session.Config{Address: *address, SkipTCPBootstrap: *skipTCP, RequestTimeout: *timeout})
	if err != nil {
		return err
	}
	defer client.Close()
	if command.Flags&0x80 != 0 || command.Flags&0x60 == 0 {
		if err = client.Send(ctx, command); err != nil {
			return err
		}
		return output(map[string]any{"sent": true, "response_requested": false, "camera_acceptance": "not-established"})
	}
	reply, err := client.Request(ctx, command)
	if err != nil {
		return err
	}
	return output(map[string]any{"sender": reply.Sender, "receiver": reply.Receiver, "sequence": reply.Sequence, "flags": reply.Flags, "set": reply.CommandSet, "id": reply.CommandID, "payload_hex": hex.EncodeToString(reply.Payload)})
}
