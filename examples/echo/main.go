// Command echo runs a Verification SCP on a loopback port and sends it a
// C-ECHO, so the whole handshake is visible in one process.
//
//	go run ./examples/echo
//
// Starting both ends of an association is how these examples work: there is no
// configuration to write and no second terminal to keep open, and what the
// program prints is what went over the socket.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/godicom-dev/gonetdicom/ae"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() { _ = ae.Serve(ctx, ln, ae.ServerConfig{AETitle: "ECHO_SCP"}) }()

	// An empty PresentationContexts list proposes Verification only, which is
	// all C-ECHO needs.
	assoc, err := ae.Dial(ctx, ae.Config{AETitle: "ECHO_SCU"}, ln.Addr().String(), "ECHO_SCP")
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = assoc.Release(context.Background()) }()

	if err := assoc.CEcho(ctx); err != nil {
		log.Fatalf("C-ECHO: %v", err)
	}
	fmt.Printf("C-ECHO to ECHO_SCP at %s: ok\n", ln.Addr())
}
