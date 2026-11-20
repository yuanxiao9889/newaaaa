package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/net/publicsuffix"
)

// Only original image GETs use this transport. The HTTP client applies it anew
// to each redirect, keeping the signed URL and redirect base on the original host.
type imageResultTransport struct {
	primary   http.RoundTripper
	proxy     string
	secondary ssrfResolver
}

func getImageResult(assetURL, proxy string, base *http.Client) (*http.Response, error) {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, common.GetEnvOrDefaultString("IMAGE_RESULT_DNS_FALLBACK", "8.8.8.8:53"))
	}}
	client.Transport = &imageResultTransport{primary: base.Transport, proxy: proxy, secondary: resolver}
	// RoundTrip checks the URL on every hop, including the initial request.
	client.CheckRedirect = nil // net/http's default ten-redirect limit
	return client.Get(assetURL)
}

func (t *imageResultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	err := ValidateSSRFProtectedFetchURL(req.URL.String())
	if err == nil {
		primary := t.primary
		if primary == nil {
			primary = http.DefaultTransport
		}
		var resp *http.Response
		resp, err = primary.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
	} else {
		err = fmt.Errorf("request blocked: %w", err)
	}

	host := strings.TrimSuffix(req.URL.Hostname(), ".")
	var dnsErr *net.DNSError
	if req.Method != http.MethodGet || !errors.As(err, &dnsErr) || !dnsErr.IsNotFound ||
		!strings.EqualFold(strings.TrimSuffix(dnsErr.Name, "."), host) || net.ParseIP(host) != nil {
		return nil, err
	}
	// Do not disclose local search names to public DNS. Query the absolute
	// name so the secondary resolver cannot apply the system search list.
	lastDot := strings.LastIndexByte(host, '.')
	if lastDot < 0 {
		return nil, err
	}
	if _, icann := publicsuffix.PublicSuffix(strings.ToLower(host[lastDot+1:])); !icann {
		return nil, err
	}
	return t.downloadWithSecondaryDNS(req, host)
}

func (t *imageResultTransport) downloadWithSecondaryDNS(req *http.Request, host string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), 4*time.Second)
	defer cancel()
	ips, lookupErr := t.secondary.LookupIPAddr(ctx, host+".")
	if lookupErr != nil {
		return nil, fmt.Errorf("secondary image DNS for %s: %w", host, lookupErr)
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Name: host, Err: "no addresses in secondary DNS answer", IsNotFound: true}
	}

	policy, enabled, policyErr := currentFetchProtection()
	if policyErr != nil {
		return nil, policyErr
	}
	publicOnly := common.SSRFProtection{}
	for _, addr := range ips {
		if check := publicOnly.ValidateResolvedIP(host, addr.IP); check != nil {
			return nil, fmt.Errorf("request blocked: %w", check)
		}
		if enabled {
			if check := policy.ValidateResolvedIP(host, addr.IP); check != nil {
				return nil, fmt.Errorf("request blocked: %w", check)
			}
		}
	}

	// Select the same egress route using the original hostname (including NO_PROXY).
	var proxyURL *url.URL
	var err error
	if strings.TrimSpace(t.proxy) != "" {
		proxyURL, _, err = common.ParseProxyURLRuntime(t.proxy)
	} else {
		proxyURL, err = http.ProxyFromEnvironment(req)
	}
	if err != nil {
		return nil, err
	}
	port := req.URL.Port()
	if port == "" {
		port = "443"
		if req.URL.Scheme == "http" {
			port = "80"
		}
	}
	tr := newRelayHTTPTransport()
	tr.Proxy = nil
	tr.DisableKeepAlives = true
	tr.TLSClientConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if proxyURL != nil {
		if err := configureProxyTransport(tr, proxyURL); err != nil {
			return nil, err
		}
		if proxyURL.Scheme == "https" {
			// The first TLS handshake authenticates the proxy. The one inside
			// CONNECT authenticates the original image hostname.
			d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12}}
			tr.DialTLSContext = d.DialContext
		}
	}
	defer tr.CloseIdleConnections()
	for _, addr := range ips {
		pinned := req.Clone(req.Context())
		pinned.Host = req.URL.Host
		pinned.URL.Host = net.JoinHostPort(addr.IP.String(), port)
		if req.URL.Scheme == "http" && proxyURL != nil && (proxyURL.Scheme == "http" || proxyURL.Scheme == "https") {
			// In absolute-form a forward proxy routes by the request target.
			// Keep it pinned independently of the original Host header.
			pinned.URL.Opaque = "//" + pinned.URL.Host + req.URL.EscapedPath()
			if req.URL.EscapedPath() == "" {
				pinned.URL.Opaque += "/"
			}
		}
		resp, dialErr := tr.RoundTrip(pinned)
		if dialErr == nil {
			common.SysLog(fmt.Sprintf("image result DNS fallback: host=%s status=%d", host, resp.StatusCode))
			return resp, nil
		}
		err = dialErr
		if req.Context().Err() != nil {
			break
		}
	}
	return nil, err
}
