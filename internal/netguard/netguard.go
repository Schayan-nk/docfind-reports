// Package netguard decides whether docfind may connect to an address that
// somebody typed.
//
// SERVER-SIDE REQUEST FORGERY. Several settings name a host the server then
// connects to: a person's own mailbox, the AI provider's address, a report
// builder connection. Each one is a way to make the server open a connection
// its user could not -- to its own loopback services, to another machine on
// the office LAN, to a cloud metadata address. So every such host is checked
// here, by the ADDRESSES IT RESOLVES TO, not by its spelling: "localhost",
// "127.1", "0x7f000001" and a DNS name pointing at 127.0.0.1 are one address.
//
// Three policies, from strictest:
//
//	Public     only addresses on the internet. For hosts an ordinary user
//	           sets (their own mailbox): nothing inside the office.
//	LAN        the office network is fine; loopback, link-local (including
//	           169.254.169.254, the metadata address), multicast and
//	           "this host" are not. For hosts an administrator sets.
//	Loopback   LAN plus this machine -- for a local model server (Ollama)
//	           an administrator runs beside docfind.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Policy is how far a destination may be from the public internet.
type Policy int

const (
	Public Policy = iota
	LAN
	Loopback
)

// ErrRefused is wrapped by every refusal.
var ErrRefused = errors.New("netguard: destination not allowed")

// ErrUnresolved is wrapped, alongside ErrRefused, when the name did not
// resolve here. A caller whose traffic leaves through a proxy -- which
// resolves names itself -- may accept that; the dial hook still checks
// whatever address is actually connected to.
var ErrUnresolved = errors.New("netguard: host does not resolve")

// Resolver is net.DefaultResolver, replaceable in tests.
var Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
} = net.DefaultResolver

// CheckHost resolves host and refuses it if ANY address it resolves to is
// outside policy -- a name with one public and one private address is
// refused, because the dialler may pick either. allow names hosts that are
// accepted whatever they resolve to (the office's own mail server, say).
func CheckHost(ctx context.Context, host string, policy Policy, allow ...string) error {
	host = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return fmt.Errorf("%w: no host", ErrRefused)
	}
	for _, a := range allow {
		if a != "" && strings.EqualFold(host, strings.TrimSpace(a)) {
			return nil
		}
	}
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		addrs, err = Resolver.LookupNetIP(cctx, "ip", host)
		if err != nil || len(addrs) == 0 {
			return fmt.Errorf("%w: %s: %w", ErrRefused, host, ErrUnresolved)
		}
	}
	for _, ip := range addrs {
		if why := refuse(ip.Unmap(), policy); why != "" {
			return fmt.Errorf("%w: %s is %s", ErrRefused, host, why)
		}
	}
	return nil
}

func refuse(ip netip.Addr, policy Policy) string {
	switch {
	case !ip.IsValid():
		return "not an address"
	case ip.IsUnspecified():
		return "the unspecified address"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return "link-local (this includes the cloud metadata address)"
	case ip.IsMulticast() || ip.IsInterfaceLocalMulticast():
		return "multicast"
	case ip.IsLoopback():
		if policy >= Loopback {
			return ""
		}
		return "this machine"
	case ip.IsPrivate() || cgnat.Contains(ip):
		if policy >= LAN {
			return ""
		}
		return "inside the office network"
	}
	return ""
}

// 100.64.0.0/10, carrier-grade NAT: not "private" to the standard library,
// but not the internet either.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")
