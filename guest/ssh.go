package guest

import (
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/lab47/isle/network"
	gossh "github.com/lab47/isle/pkg/crypto/ssh"
	"github.com/lab47/isle/pkg/ssh"
)

// direct-tcpip data struct as specified in RFC4254, Section 7.2
type localForwardChannelData struct {
	DestAddr string
	DestPort uint32

	OriginAddr string
	OriginPort uint32
}

// DirectTCPIPHandler can be enabled by adding it to the server's
// ChannelHandlers under direct-tcpip.
func (g *Guest) DirectTCPIPHandler(srv *ssh.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
	d := localForwardChannelData{}
	if err := gossh.Unmarshal(newChan.ExtraData(), &d); err != nil {
		newChan.Reject(gossh.ConnectionFailed, "error parsing forward data: "+err.Error())
		return
	}

	name := g.oneAndOnlyIsle()
	if name == "" {
		name = conn.User()
	}

	cont, err := g.lookupContainer(ctx, name)
	if err != nil {
		fmt.Printf("container not running: %s -- %s\n", name, err)
		newChan.Reject(gossh.ConnectionFailed, "container not running: "+name)
		return
	}

	task, err := cont.Task(ctx, nil)
	if err != nil {
		fmt.Printf("container task not running: %s -- %s\n", name, err)
		newChan.Reject(gossh.ConnectionFailed, "container not running: "+name)
		return
	}

	dest := net.JoinHostPort(d.DestAddr, strconv.FormatInt(int64(d.DestPort), 10))

	fmt.Printf("connecting to %s\n", dest)

	dconn, err := network.DialTCP(ctx, int(task.Pid()), dest)
	if err != nil {
		fmt.Printf("unable to dial: %s\n", err)
		newChan.Reject(gossh.ConnectionFailed, err.Error())
		return
	}

	ch, reqs, err := newChan.Accept()
	if err != nil {
		fmt.Printf("unable to accept: %s\n", err)
		dconn.Close()
		return
	}
	go gossh.DiscardRequests(reqs)

	fmt.Printf("forwarding...\n")
	go func() {
		defer fmt.Printf("closed connection 1 to %s\n", dest)
		defer ch.Close()
		defer dconn.Close()
		io.Copy(ch, dconn)
	}()
	go func() {
		defer fmt.Printf("closed connection 2 to %s\n", dest)
		defer ch.Close()
		defer dconn.Close()
		io.Copy(dconn, ch)
	}()
}
