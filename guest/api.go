package guest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/locator"
	"github.com/lab47/isle/types"
	"google.golang.org/grpc"
)

func (g *Guest) startAPI(ctx context.Context) error {
	serv := grpc.NewServer()

	guestapi.RegisterGuestAPIServer(serv, g)

	g.L.Info("starting api listener")

	li, err := net.Listen("tcp", "0.0.0.0:1212")
	if err != nil {
		return err
	}

	return serv.Serve(li)
}

func (g *Guest) AddApp(ctx context.Context, req *guestapi.AddAppReq) (*guestapi.AddAppResp, error) {
	if req.Name == "" || req.Selector == "" {
		return nil, fmt.Errorf("invalid request, require name and selector")
	}

	oldName := filepath.Join("/share/home/linux-apps", req.Name)
	if _, err := os.Stat(oldName); err == nil {
		err := g.validateApp(ctx, oldName)
		if err != nil {
			return nil, err
		}

		err = os.Symlink(oldName, filepath.Join("/data/apps/running", req.Name))
		if err != nil {
			return nil, err
		}

		var resp guestapi.AddAppResp
		return &resp, nil
	}

	cfg, data, err := locator.Fetch(ctx, req.Selector)
	if err != nil {
		return nil, err
	}

	availDir := filepath.Join("/data/apps/available", cfg.Name)

	err = os.MkdirAll(availDir, 0755)
	if err != nil {
		return nil, err
	}

	f, err := os.Create(filepath.Join(availDir, "app.hcl"))
	if err != nil {
		return nil, err
	}

	f.Write(data)

	err = f.Close()
	if err != nil {
		return nil, err
	}

	err = os.Symlink(availDir, filepath.Join("/data/apps/running", cfg.Name))
	if err != nil {
		return nil, err
	}

	var resp guestapi.AddAppResp
	return &resp, nil
}

func (g *Guest) DisableApp(ctx context.Context, req *guestapi.DisableAppReq) (*guestapi.DisableAppResp, error) {
	path := filepath.Join("/data/apps/running", req.Id)

	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("unknown app: %s", req.Id)
	}

	err := os.Remove(path)
	if err != nil {
		return nil, err
	}

	var resp guestapi.DisableAppResp
	return &resp, nil
}

func (g *Guest) sendToHost(ctx context.Context, kind string, req interface{}, resp interface{}) error {
	sess := g.currentSession
	host, err := sess.Open()
	if err != nil {
		g.L.Error("error opening connection to host", "error", err)
		return err
	}

	enc := cbor.NewEncoder(host)
	dec := cbor.NewDecoder(host)

	err = enc.Encode(types.HeaderMessage{Kind: kind})
	if err != nil {
		g.L.Error("error encoding message to host", "error", err)
		return err
	}

	err = enc.Encode(req)
	if err != nil {
		g.L.Error("error encoding message to host", "error", err)
		return err
	}

	var respm types.ResponseMessage

	err = dec.Decode(&respm)
	if err != nil {
		g.L.Error("error decoding message to host", "error", err)
		return err
	}

	if respm.Code != types.OK {
		g.L.Error("host reported error canceling port", "error", respm.Error)
		return fmt.Errorf("remote error: %s", respm.Error)
	}

	if resp == nil {
		return nil
	}

	return dec.Decode(&resp)
}

func (g *Guest) RunOnMac(s guestapi.GuestAPI_RunOnMacServer) error {
	c, err := g.hostAPI().RunOnMac(s.Context())
	if err != nil {
		return err
	}

	go func() {
		for {
			m, err := c.Recv()
			if err != nil {
				return
			}

			err = s.Send(m)
			if err != nil {
				return
			}
		}
	}()

	for {
		m, err := s.Recv()
		if err != nil {
			return err
		}

		err = c.Send(m)
		if err != nil {
			return err
		}
	}
}

func (g *Guest) TrimMemory(ctx context.Context, req *guestapi.TrimMemoryReq) (*guestapi.TrimMemoryResp, error) {
	return g.hostAPI().TrimMemory(ctx, req)
}

func (v *Guest) Console(s guestapi.GuestAPI_ConsoleServer) error {
	in, err := s.Recv()
	if err != nil {
		return err
	}

	cmd := exec.Command(in.Command[0], in.Command[1:]...)
	cmd.Env = os.Environ()
	cmd.Dir = "/"

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	go func() {
		for {
			m, err := s.Recv()
			if err != nil {
				return
			}

			if m.Closed {
				stdin.Close()
				return
			}

			stdin.Write(m.Input)
		}
	}()

	buf := make([]byte, 1024)

	for {
		n, _ := stdout.Read(buf)
		if n == 0 {
			break
		}

		err := s.Send(&guestapi.RunOutput{
			Data: buf[:n],
		})
		if err != nil {
			break
		}
	}

	var exit int

	err = cmd.Wait()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return err
		}
	}

	s.Send(&guestapi.RunOutput{
		Closed:   true,
		ExitCode: int32(exit),
	})

	return nil
}
