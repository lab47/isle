package guest

import (
	"context"
	"strconv"

	"github.com/hashicorp/go-hclog"
	"github.com/miekg/dns"
)

type handler struct {
	L hclog.Logger

	config *dns.ClientConfig
	client *dns.Client

	conns []*dns.Conn
}

func (this *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	this.L.Info("processing dns request", "question", r.Question[0].String(), "client", w.RemoteAddr())

	for _, conn := range this.conns {
		msg, _, err := this.client.ExchangeWithConn(r, conn)
		if err == nil {
			if len(msg.Answer) == 0 {
				this.L.Info("no answer detected")
			} else {
				this.L.Info("responding to request", "answer", msg.Answer[0].String())
			}
			w.WriteMsg(msg)
			return
		}
	}

	this.L.Info("no upstreams could resolve a dns query")
}

func StartDNS(ctx context.Context, log hclog.Logger) error {
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return err
	}

	client := &dns.Client{Net: "udp"}

	var conns []*dns.Conn

	for _, server := range config.Servers {
		conn, err := client.Dial(server + ":" + config.Port)
		if err == nil {
			conns = append(conns, conn)
		}
	}

	srv := &dns.Server{Addr: ":" + strconv.Itoa(53), Net: "udp"}
	srv.Handler = &handler{
		L:      log,
		config: config,
		client: client,
		conns:  conns,
	}

	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()

	return srv.ListenAndServe()
}
