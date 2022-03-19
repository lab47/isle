package yalr4m

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/yalr4m/types"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
)

type CLI struct {
	Name   string
	Image  string
	Dir    string
	AsRoot bool

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

	for i := 0; i < 100; i++ {
		c.L.Info("connecting to unix socket")
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: c.Path})
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		c.L.Info("negotiating ssh")

		sc, chans, reqs, err = ssh.NewClientConn(conn, "vsock", &cfg)
		if err != nil {
			conn.Close()
			time.Sleep(time.Second)
			continue
		}

		break
	}

	sconn := ssh.NewClient(sc, chans, reqs)

	sess, err := sconn.NewSession()

	sess.Stdout = stdout
	sess.Stderr = stdout
	sess.Stdin = stdin

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

	if cmd == "" {
		c.L.Info("running shell")

		state, err := terminal.MakeRaw(int(os.Stdout.Fd()))
		if err == nil {
			defer terminal.Restore(int(os.Stdout.Fd()), state)
		}

		err = sess.Shell()
	} else {
		c.L.Info("running command", "command", cmd)
		err = sess.Start(cmd)
	}

	if err != nil {
		return err
	}

	return sess.Wait()
}
