package vm

import (
	"context"
	"net"
	"sync"

	"github.com/Code-Hex/vz/v3"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/miekg/dns"
)

type dnsHandler struct {
	L hclog.Logger

	config *dns.ClientConfig
	client *dns.Client

	addresses []string

	configOnce sync.Once
}

func (h *dnsHandler) updateConfig() {
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

func (h *dnsHandler) resolveLocal(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) bool {
	if len(r.Question) != 1 {
		return false
	}

	q := r.Question[0]

	var resp dns.Msg
	resp.SetReply(r)

	hdr := dns.RR_Header{
		Name:   q.Name,
		Rrtype: q.Qtype,
		Class:  q.Qclass,
		Ttl:    60,
	}

	switch q.Qtype {
	case dns.TypeA:
		var nr net.Resolver
		nr.PreferGo = false

		if ips, err := nr.LookupIP(ctx, "ip4", q.Name); err == nil {
			for _, ip := range ips {
				// Don't include host loopback addresses, they aren't meaningful
				// to the VM.
				if ip.IsLoopback() {
					continue
				}
				resp.Answer = append(resp.Answer, &dns.A{
					Hdr: hdr,
					A:   ip,
				})
			}
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	case dns.TypeAAAA:
		var nr net.Resolver
		nr.PreferGo = false

		if ips, err := nr.LookupIP(ctx, "ip6", q.Name); err == nil {
			for _, ip := range ips {
				// Don't include host loopback addresses, they aren't meaningful
				// to the VM.
				if ip.IsLoopback() {
					continue
				}
				resp.Answer = append(resp.Answer, &dns.AAAA{
					Hdr:  hdr,
					AAAA: ip,
				})
			}
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	case dns.TypeCNAME:
		var nr net.Resolver
		nr.PreferGo = false

		if tgt, err := nr.LookupCNAME(ctx, q.Name); err == nil {
			resp.Answer = append(resp.Answer, &dns.CNAME{
				Hdr:    hdr,
				Target: tgt,
			})
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	case dns.TypeNS:
		var nr net.Resolver
		nr.PreferGo = false

		if nss, err := nr.LookupNS(ctx, q.Name); err == nil {
			for _, ns := range nss {
				resp.Answer = append(resp.Answer, &dns.NS{
					Hdr: hdr,
					Ns:  ns.Host,
				})
			}
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	case dns.TypeMX:
		var nr net.Resolver
		nr.PreferGo = false

		if recs, err := nr.LookupMX(ctx, q.Name); err == nil {
			var resp dns.Msg
			resp.SetReply(r)

			for _, rec := range recs {
				resp.Answer = append(resp.Answer, &dns.MX{
					Hdr: hdr,
					Mx:  rec.Host,
				})
			}
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	case dns.TypeSRV:
		var nr net.Resolver
		nr.PreferGo = false

		if _, recs, err := nr.LookupSRV(ctx, "", "", q.Name); err == nil {
			for _, rec := range recs {
				resp.Answer = append(resp.Answer, &dns.SRV{
					Hdr:      hdr,
					Priority: rec.Priority,
					Weight:   rec.Weight,
					Target:   rec.Target,
					Port:     rec.Port,
				})
			}
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	case dns.TypeTXT:
		var nr net.Resolver
		nr.PreferGo = false

		if recs, err := nr.LookupTXT(ctx, q.Name); err == nil {
			var resp dns.Msg
			resp.SetReply(r)

			resp.Answer = append(resp.Answer, &dns.TXT{
				Hdr: hdr,
				Txt: recs,
			})
		} else if dne, ok := err.(*net.DNSError); ok {
			if !dne.IsNotFound {
				return false
			}

			// ok, send an empty back.
		} else {
			return false
		}
	default:
		return false
	}

	err := w.WriteMsg(&resp)
	if err != nil {
		h.L.Error("error writing response", "error", err)
		return false
	}

	return true
}

func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	h.configOnce.Do(h.updateConfig)

	ctx := context.Background()

	if h.resolveLocal(ctx, w, r) {
		return
	}

	for i := 0; i < 10; i++ {
		for _, addr := range h.addresses {

			msg, _, err := h.client.Exchange(r, addr)
			if err == nil {
				w.WriteMsg(msg)
				return
			}
		}
	}

	h.L.Warn("no upstreams could resolve a dns query", "query", r.String())
}

func (v *VM) runDNS(sock *vz.VirtioSocketDevice) error {
	listener, err := sock.Listen(53)
	if err != nil {
		return err
	}

	go func() {
		conn, err := listener.AcceptVirtioSocketConnection()
		if err != nil {
			return
		}

		defer conn.Close()

		sess, err := yamux.Server(conn, yamux.DefaultConfig())
		if err != nil {
			v.L.Error("error creating yamux session for dns", "error", err)
			return
		}

		srv := &dns.Server{
			Handler: &dnsHandler{
				L: v.L,
			},
			Listener: sess,
		}

		srv.ActivateAndServe()
	}()

	v.L.Info("activated host dns")

	return nil
}
