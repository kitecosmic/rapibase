package push

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Web push endpoints come from app users' browsers, so they are
// untrusted URLs the server will POST to. Without a check, a user could
// register "http://<internal-host>:<port>/…" and make the instance send
// requests inside its private network (SSRF) — the database, the storage
// backend, or a cloud metadata service. Real push services are always
// public HTTPS hosts, so anything else is refused, both when the
// subscription is saved and again when the connection is made (DNS can
// change between the two).

var errNonPublicAddress = errors.New("push endpoint resolves to a non-public address")

// ValidateWebPushEndpoint checks a subscription endpoint before storing it.
func ValidateWebPushEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("endpoint must be an absolute URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint must use https")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return errNonPublicAddress
	}
	if h := strings.ToLower(host); h == "localhost" || strings.HasSuffix(h, ".localhost") || !strings.Contains(h, ".") {
		return errNonPublicAddress
	}
	return nil
}

func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// Carrier-grade NAT (100.64.0.0/10) is not public either.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return false
	}
	return true
}

// newPublicOnlyClient returns an HTTP client that refuses to connect to
// non-public addresses, checked on the resolved IP of every connection.
func newPublicOnlyClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return errNonPublicAddress
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("push endpoint redirected to a non-https URL")
			}
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}
