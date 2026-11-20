package mdns

// Optional `_jackui._tcp` discovery for native clients (issue #201 item 6).
// Prod runs inside gluetun's netns where multicast never reaches the LAN, so
// this is strictly opt-in (JACKUI_MDNS=1) and must never affect startup or
// shutdown: every failure is logged and swallowed. Manual URL entry always
// works regardless.

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/hashicorp/mdns"
)

// Enabled reports whether discovery was opted in via JACKUI_MDNS.
func Enabled() bool {
	switch os.Getenv("JACKUI_MDNS") {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Announce starts advertising the server on the local network and returns a
// stop func (never nil; safe to call more than once). host is the advertised
// instance name, port the HTTP port, baseURL the externally reachable URL
// (e.g. https://jackui.internal.lgldsilva.com.br) carried in TXT so clients
// that prefer a URL skip the IP:port reconstruction.
func Announce(instance string, port int, baseURL string) (stop func()) {
	noop := func() {
		// Deliberately empty (S1186): the disabled/no-interfaces path has
		// nothing to release — the func only keeps callers free of nil checks.
	}
	if !Enabled() {
		return noop
	}
	// The zone wants a hostname; fall back to the local machine name and,
	// when even that fails, a stable synthetic name.
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "jackui-server"
	}
	host += "."

	ips, err := net.InterfaceAddrs()
	if err != nil {
		log.Printf("[jackui-mdns] disabled: cannot enumerate interfaces: %v", err)
		return noop
	}
	var localIPs []net.IP
	for _, a := range ips {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			localIPs = append(localIPs, ipnet.IP)
		}
	}
	if len(localIPs) == 0 {
		log.Printf("[jackui-mdns] disabled: no non-loopback IPv4 address (server likely behind gluetun)")
		return noop
	}

	txt := []string{"path=/"}
	if baseURL != "" {
		txt = append(txt, "url="+baseURL)
	}

	server, err := mdns.NewMDNSService(instance, "_jackui._tcp", "", host, port, localIPs, txt)
	if err != nil {
		log.Printf("[jackui-mdns] disabled: service setup failed: %v", err)
		return noop
	}
	srv, err := mdns.NewServer(&mdns.Config{Zone: server})
	if err != nil {
		log.Printf("[jackui-mdns] disabled: server start failed: %v", err)
		return noop
	}
	log.Printf("[jackui-mdns] advertising %s on port %s (%d IPv4 interfaces)", instance, strconv.Itoa(port), len(localIPs))
	once := false
	return func() {
		if once {
			return
		}
		once = true
		if err := srv.Shutdown(); err != nil {
			log.Printf("[jackui-mdns] shutdown: %v", err)
		}
	}
}

// BaseURLForAnnounce picks the URL to advertise: the configured external base
// URL when set, else a LAN-URL reconstructed from the first private IPv4.
func BaseURLForAnnounce(baseURL string, port int) string {
	if baseURL != "" {
		return baseURL
	}
	ips, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range ips {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil && ipnet.IP.IsPrivate() {
			// LAN discovery advertises the plain-http URL on purpose: the
			// private network has no TLS terminator, and clients fall back to
			// manual https:// entry when the operator fronts it with TLS.
			return fmt.Sprintf("http://%s:%d", ipnet.IP.String(), port) //NOSONAR: S5332 — intranet discovery URL, no TLS terminator by design
		}
	}
	return ""
}
