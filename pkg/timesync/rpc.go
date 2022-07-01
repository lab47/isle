package timesync

import (
	"context"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/pkg/pbstream"
)

func NewClient(log hclog.Logger, client PBSTimeSyncClient) *Client {
	return &Client{log, client}
}

type Client struct {
	log    hclog.Logger
	client PBSTimeSyncClient
}

func (c *Client) Sync(ctx context.Context, fn func(hclog.Logger, int64, int64) error) error {
	stream := c.client.TimeSync(ctx)

	tick := time.NewTicker(64 * time.Second)
	defer tick.Stop()

	for {
		base := time.Now().UnixNano()
		var pkt NTPTimePacket
		pkt.T1 = base

		err := stream.Send(&pkt)
		if err != nil {
			c.log.Error("error sending time packet", "error", err)
			return err
		}

		p, err := stream.Receive()
		if err != nil {
			c.log.Error("error recieving time packet", "error", err)
			return err
		}

		now := time.Now()
		p.T4 = now.UnixNano()

		offset := ((p.T2 - p.T1) + (p.T3 - p.T4)) / 2

		err = fn(c.log, base, offset)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			// ok, run another sync
		}
	}
}

func NewServerHandler(log hclog.Logger) pbstream.Handler {
	_, h := PBSNewTimeSyncHandler(&Server{log: log})
	return h
}

type Server struct {
	log hclog.Logger
}

func (s *Server) TimeSync(ctx context.Context, stream *pbstream.BidiStream[NTPTimePacket, NTPTimePacket]) error {
	s.log.Info("handling ntp time pulses")

	for {
		ri, err := stream.ReceiveInfo()
		if err != nil {
			return err
		}

		p := ri.Value

		p.T2 = ri.ReceiveTime.UnixNano()
		p.T3 = time.Now().UnixNano()

		err = stream.Send(p)
		if err != nil {
			return err
		}
	}
}
