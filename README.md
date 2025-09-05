# IAX2 Go Library

A Go implementation of the IAX2 (Inter-Asterisk eXchange version 2) protocol for VoIP communications. This library provides a complete client implementation for connecting to Asterisk servers and managing voice calls.

## Features

- **Full IAX2 Protocol Support**: Complete implementation of call control, registration, and media handling
- **Multi-Codec Support**: ULAW, ALAW, G.729, GSM, Speex, and more
- **Authentication**: MD5 challenge/response and call token support
- **Media Streaming**: Real-time audio streaming
- **DTMF Support**: Send and receive touch-tone signals
- **Call Management**: Handle incoming/outgoing calls with full state control
- **Peer Registration**: Dynamic peer registration and management

## Quick Start

```go
package main

import (
    "log"
    "time"
    "github.com/jaracil/iax"
)

func main() {
    // Create IAX trunk
    opts := &iax.IAXTrunkOptions{
        BindAddr: "0.0.0.0:4569",
    }
    trunk := iax.NewIAXTrunk(opts)
    
    // Add peer
    trunk.AddPeer(&iax.Peer{
        User:       "username",
        Password:   "password",
        Host:       "asterisk-server:4569",
        CodecPrefs: []iax.Codec{iax.CODEC_ULAW},
        CodecCaps:  iax.CODEC_ULAW.BitMask(),
    })
    
    // Initialize trunk
    if err := trunk.Init(); err != nil {
        log.Fatalf("Failed to initialize trunk: %v", err)
    }
    
    // Handle events
    for {
        event, err := trunk.WaitEvent(time.Second * 10)
        if err != nil {
            if err == iax.ErrTimeout {
                continue
            }
            log.Printf("Event error: %v", err)
            break
        }
        
        switch evt := event.(type) {
        case *iax.IncomingCallEvent:
            // Handle incoming call
            call := evt.Call
            if err := call.Accept(iax.CODEC_ULAW); err != nil {
                log.Printf("Failed to accept call: %v", err)
                continue
            }
            if err := call.Answer(); err != nil {
                log.Printf("Failed to answer call: %v", err)
                continue
            }
        case *iax.RegistrationEvent:
            log.Printf("Registration event: %+v", evt)
        }
    }
}
```

## Examples

See the `examples/` directory for complete working examples including:
- Basic IAX2 client with call handling
- Media playback and DTMF processing
- Peer registration and management

## Requirements

- Go 1.21.6 or later
- Network connectivity to IAX2 server (typically Asterisk)

## License

This project is licensed under the MIT License.
