package guest

import (
	"os"
	"os/exec"

	"github.com/lab47/isle/guestapi"
)

type RunStreamer interface {
	Send(*guestapi.RunOutput) error
	Recv() (*guestapi.RunInput, error)
}

func RunCommand(s RunStreamer) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	in, err := s.Recv()
	if err != nil {
		return err
	}

	cmd := exec.Command(in.Command[0], in.Command[1:]...)
	cmd.Env = os.Environ()
	cmd.Dir = homeDir

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
