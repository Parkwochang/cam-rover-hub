package rover

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NormalizeAddress accepts only a private LAN IP or a .local discovery name.
// Never accept URL credentials, paths or external hosts from the settings UI.
func NormalizeAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("use a private LAN IP or a .local hostname, without credentials or paths")
	}
	host := strings.ToLower(u.Hostname())
	ip, ipErr := netip.ParseAddr(host)
	if ipErr == nil {
		if !ip.Is4() || !ip.IsPrivate() {
			return "", errors.New("rover IP must be a private IPv4 address")
		}
	} else {
		if !strings.HasSuffix(host, ".local") || len(host) > 253 {
			return "", errors.New("rover hostname must end in .local")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", errors.New("invalid rover hostname")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", errors.New("invalid rover hostname")
				}
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid rover port")
	}
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid rover port")
		}
	}
	if port != "" && port != "80" {
		host = net.JoinHostPort(host, port)
	}
	return "http://" + host, nil
}

func resolveLocal(ctx context.Context, host string) (string, error) {
	// Static CGO-disabled Go builds do not use Linux's libnss_mdns. Delegate
	// .local lookup to the system NSS resolver (Avahi), with a strict deadline.
	if runtime.GOOS != "linux" || !strings.HasSuffix(strings.ToLower(host), ".local") {
		return host, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	data, err := exec.CommandContext(ctx, "getent", "ahostsv4", host).Output()
	if err != nil {
		return "", errors.New("mDNS lookup failed; check Avahi/libnss-mdns or set a reserved LAN IP")
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		ip, err := netip.ParseAddr(fields[0])
		if err == nil && ip.Is4() && ip.IsPrivate() {
			return ip.String(), nil
		}
	}
	return "", errors.New("mDNS returned no private IPv4 address")
}

type localResolver struct {
	mu       sync.Mutex
	host, ip string
	expires  time.Time
	revision uint64
	lookup   func(context.Context, string) (string, error)
}

func (r *localResolver) invalidate() {
	r.mu.Lock()
	r.expires = time.Time{}
	r.revision++
	r.mu.Unlock()
}

func (r *localResolver) resolve(ctx context.Context, host string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(host), ".local") {
		return host, nil
	}
	r.mu.Lock()
	if r.host == host && time.Now().Before(r.expires) {
		ip := r.ip
		r.mu.Unlock()
		return ip, nil
	}
	revision := r.revision
	r.mu.Unlock()
	lookup := r.lookup
	if lookup == nil {
		lookup = resolveLocal
	}
	ip, err := lookup(ctx, host)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.revision == revision {
		r.host, r.ip, r.expires = host, ip, time.Now().Add(2*time.Second)
	}
	r.mu.Unlock()
	return ip, nil
}

func (r *localResolver) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host, err = r.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
	if err != nil {
		r.invalidate()
	}
	return conn, err
}
