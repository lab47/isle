package guest

import (
	"context"
	"strconv"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/go-hclog"
	"github.com/miekg/dns"
)

type handler struct {
	L hclog.Logger

	config *dns.ClientConfig
	client *dns.Client

	addresses []string

	configOnce sync.Once
}

func (h *handler) updateConfig() {
	config, _ := dns.ClientConfigFromFile("/etc/resolv.conf")

	if config == nil || len(config.Servers) == 0 {
		h.L.Warn("no system resolv.conf available, using 8.8.8.8")

		config = &dns.ClientConfig{
			Servers: []string{"8.8.8.8", "4.2.2.1"},
			Port:    "53",
		}
	}

	client := &dns.Client{Net: "udp"}

	for _, server := range config.Servers {
		h.addresses = append(h.addresses, server+":"+config.Port)
	}

	h.config = config
	h.client = client
}

func (h *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	h.configOnce.Do(h.updateConfig)

	h.L.Debug("processing dns request", "question", r.Question[0].String(), "client", w.RemoteAddr())

	for i := 0; i < 10; i++ {
		for _, addr := range h.addresses {
			msg, _, err := h.client.Exchange(r, addr)
			if err == nil {
				if len(msg.Answer) == 0 {
					h.L.Debug("no answer detected")
				} else {
					h.L.Debug("responding to request", "answer", msg.Answer[0].String())
				}
				w.WriteMsg(msg)
				return
			}
		}
	}

	h.L.Warn("no upstreams could resolve a dns query", "query", r.String())
}

func StartDNS(ctx context.Context, log hclog.Logger) error {
	h := &handler{
		L: log,
	}

	// Pickup any changes to resolv.conf automatically
	go func() {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			return
		}

		w.Add("/etc/resolv.conf")

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.Events:
				h.updateConfig()
			}
		}
	}()

	srv := &dns.Server{Addr: ":" + strconv.Itoa(53), Net: "udp"}
	srv.Handler = h

	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()

	return srv.ListenAndServe()
}
