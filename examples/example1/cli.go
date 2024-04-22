package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jaracil/iax"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	opts := &iax.IAXTrunkOptions{
		BindAddr:        "0.0.0.0:10012", // Optional
		DebugMiniframes: false,
	}

	cli := iax.NewIAXTrunk(opts)
	cli.SetLogLevel(iax.DebugLogLevel)

	cli.AddPeer(&iax.Peer{
		User:            "line0",
		Password:        "line0_123",
		Host:            "127.0.0.1:4569",
		RegOutInterval:  time.Second * 60,
		CodecPrefs:      []iax.Codec{iax.CODEC_ULAW},
		CodecCaps:       iax.CODEC_ULAW.BitMask(),
		EnableCallToken: true,
	})

	err := cli.Init()
	if err != nil {
		panic(err)
	}

	time.Sleep(time.Second)
	cli.Poke("line0") // check if peer is online

	for {
		cliEvt, err := cli.WaitEvent(time.Second * 10)
		if err != nil {
			if err == iax.ErrTimeout {
				continue
			}
			fmt.Printf("Exit: %s\n", err)
			break
		}
		switch evt := cliEvt.(type) {
		case *iax.IncomingCallEvent:
			call := evt.Call
			go processCall(call)
		case *iax.RegistrationEvent:
			fmt.Printf("Registration: %+v\n", evt)
		case *iax.IAXTrunkStateChangeEvent:
			fmt.Printf("trunk state: %s\n", evt.State)
			if evt.State == iax.Uninitialized {
				break
			}
		}
	}
}

func waitState(call *iax.Call, state iax.CallState) error {
	for call.State() != state {
		ev, err := call.WaitEvent(time.Second)
		if err != nil {
			if err == iax.ErrTimeout {
				continue
			}
			return err
		}
		switch v := ev.(type) {
		case *iax.CallCtlEvent:
			fmt.Printf("CallCtlEvent: %s\n", iax.SubclassToString(iax.FrmControl, v.Subclass))
		}
		if call.State() == iax.HangupCallState {
			return iax.ErrRemoteHangup
		}
	}
	return nil
}

func processCall(call *iax.Call) {
	for call.State() == iax.IdleCallState {
		call.WaitEvent(time.Second)
	}
	if call.State() == iax.HangupCallState {
		return
	}
	if !call.IsOutgoing() {
		err := call.Accept(iax.CODEC_ULAW)
		if err != nil {
			return
		}
	}
	err := waitState(call, iax.AcceptCallState)
	if err != nil {
		fmt.Printf("Error waiting for Accept state: %s\n", err)
		return
	}
	if !call.IsOutgoing() {
		time.Sleep(1 * time.Second)
		_, err = call.SendPing()
		if err != nil {
			return
		}
		lag, err := call.SendLagRqst()
		if err != nil {
			return
		}
		fmt.Printf("Lag: %s\n", lag.String())

		err = call.SendProceeding()
		if err != nil {
			return
		}
		err = call.SendRinging()
		if err != nil {
			return
		}
		time.Sleep(time.Second * 2)
		err = call.Answer()
		if err != nil {
			return
		}
	} else {
		err := waitState(call, iax.OnlineCallState)
		if err != nil {
			fmt.Printf("Error waiting for Online state: %s\n", err)
			return
		}
	}
	echo := false
	for call.State() == iax.OnlineCallState {
		evt, err := call.WaitEvent(time.Second)
		if err != nil {
			if err == iax.ErrTimeout {
				continue
			}
			fmt.Printf("Error waiting for event: %s\n", err)
			break
		}
		switch v := evt.(type) {
		case *iax.DTMFEvent:
			if v.Start {
				break
			}
			fmt.Printf("DTMF: %s\n", v.Digit)
			switch v.Digit {
			case "*":
				call.Hangup("Asterisk DTMF pressed", 16)
			case "1":
				go func() {
					d, err := os.ReadFile("./vm.ulaw")
					if err != nil {
						fmt.Printf("Error opening file: %s\n", err)
						return
					}
					d = append(d, make([]byte, 320)...) // Add silence
					f := bytes.NewReader(d)
					fmt.Printf("Playing file\n")
					err = call.PlayMedia(f)
					if err != nil {
						fmt.Printf("Play media error: %s\n", err)
					}
					fmt.Printf("File played\n")
				}()
			case "2":
				call.StopMedia()
			case "5":
				echo = !echo
				fmt.Printf("Echo: %t\n", echo)
			case "#":
				call.IAXTrunk().ShutDown()
			case "3":
				go func() {
					fmt.Printf("waiting 5 seconds to dial 300 ext\n")
					time.Sleep(time.Second * 5)
					outCall := iax.NewCall(call.IAXTrunk())
					dopts := &iax.DialOptions{
						CalledNumber:  "300",
						CallingNumber: "line0",
						CodecFormat:   iax.CODEC_ULAW,
					}
					err := outCall.Dial("line0", dopts)
					if err != nil {
						fmt.Printf("Error dialing: %s\n", err)
						return
					}
					processCall(outCall)
				}()

			}
		case *iax.MediaEvent:
			if echo {
				call.SendMedia(v)
			}
		case *iax.CallStateChangeEvent:
			fmt.Printf("State: %s\n", v.State)
		case *iax.PlayEvent:
			fmt.Printf("Playing audio: %t\n", v.Playing)
		}
	}
	fmt.Printf("Call loop ended\n")
}
