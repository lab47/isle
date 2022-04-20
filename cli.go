package isle

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/pkg/crypto/ssh"
	"github.com/lab47/isle/pkg/crypto/ssh/terminal"
	"github.com/lab47/isle/types"
	"github.com/morikuni/aec"
	"golang.org/x/sys/unix"
)

type CLI struct {
	Name   string
	Image  string
	Dir    string
	AsRoot bool
	IsTerm bool

	L    hclog.Logger
	Path string
}

func (c *CLI) Shell(cmd string, stdin io.Reader, stdout io.Writer) error {
	var (
		sc    ssh.Conn
		chans <-chan ssh.NewChannel
		reqs  <-chan *ssh.Request
	)

	var cfg ssh.ClientConfig
	cfg.HostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		return nil
	}
	cfg.SetDefaults()

	var (
		conn net.Conn
		err  error
	)

	for i := 0; i < 100; i++ {
		if c.IsTerm {
			fmt.Printf("🐚 Connecting...%s",
				aec.EmptyBuilder.Column(0).ANSI.String(),
			)
		}

		c.L.Info("connecting to local socket")
		addr, err := ioutil.ReadFile(c.Path)
		if err != nil {
			return err
		}

		conn, err = net.Dial("tcp", strings.TrimSpace(string(addr)))
		/*
			conn, err = net.DialUnix("unix", nil, &net.UnixAddr{
				Name: c.Path,
				Net:  "unix",
			})
		*/
		if err != nil {
			c.L.Error("error connecting to unixsocket", "error", err)
			time.Sleep(time.Second)
			continue
		}

		c.L.Info("negotiating ssh")

		sc, chans, reqs, err = ssh.NewClientConn(conn, "vsock", &cfg)
		if err != nil {
			c.L.Debug("issue negotiating ssh connection, trying again")
			conn.Close()
			time.Sleep(time.Second)
			continue
		}

		break
	}

	c.L.Info("connect via ssh", "remote-addr", conn.RemoteAddr())

	sconn := ssh.NewClient(sc, chans, reqs)

	sess, err := sconn.NewSession()

	sess.Stdout = stdout
	sess.Stderr = stdout
	sess.Stdin = stdin

	setup := sess.Extended(2)

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "msl"
	} else {
		idx := strings.IndexByte(hostname, '.')
		if idx != -1 {
			hostname = hostname[:idx]
		}
	}

	u, err := user.Current()
	if err == nil {
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		data, err := json.Marshal(&types.MSLInfo{
			Name:     c.Name,
			Image:    c.Image,
			Dir:      c.Dir,
			AsRoot:   c.AsRoot,
			Hostname: hostname,
			UserName: u.Username,
			UserId:   uid,
		})
		if err != nil {
			return err
		}
		sess.Setenv("_MSL_INFO", string(data))
	}

	rows, cols, err := pty.Getsize(os.Stdout)
	if err == nil {
		err = sess.RequestPty(os.Getenv("TERM"), rows, cols, nil)
		if err != nil {
			return err
		}
	}

	if c.IsTerm {
		fmt.Print(aec.EmptyBuilder.Column(0).EraseLine(aec.EraseModes.All).ANSI.String())
	}

	sigWin := make(chan os.Signal, 1)

	go func() {
		for {
			select {
			case <-sigWin:
				rows, cols, err := pty.Getsize(os.Stdout)
				if err == nil {
					sess.WindowChange(rows, cols)
				}
			}
		}
	}()

	signal.Notify(sigWin, unix.SIGWINCH)

	if cmd == "" {
		c.L.Info("running shell")

		state, err := terminal.MakeRaw(int(os.Stdout.Fd()))
		if err == nil {
			defer terminal.Restore(int(os.Stdout.Fd()), state)
		}

		go io.Copy(os.Stderr, setup)
		err = sess.Shell()
	} else {
		go io.Copy(ioutil.Discard, setup)

		c.L.Info("running command", "command", cmd)
		err = sess.Start(cmd)
	}

	if err != nil {
		return err
	}

	return sess.Wait()
}
