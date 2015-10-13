package upstream

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ironsmile/nedomi/config"
	"github.com/ironsmile/nedomi/upstream/balancing"
)

// Upstream implements the http.RoundTripper interface and is used for requests
// to all simple and advanced upstreams.
type Upstream struct {
	transport          http.RoundTripper
	getUpstreamAddress func(path string) *url.URL
}

// RoundTrip implements the http.RoundTripper interface.
func (u *Upstream) RoundTrip(req *http.Request) (*http.Response, error) {
	addr := u.getUpstreamAddress(req.URL.RequestURI())
	req.URL.Scheme = addr.Scheme
	req.URL.Host = addr.Host
	return u.transport.RoundTrip(req)
}

func getTransport(conf config.UpstreamSettings) http.RoundTripper {
	//!TODO: get all of these hardcoded values from the config
	//!TODO: use the facebook retryable transport
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 10 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableKeepAlives:   false,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 5,
	}

	if conf.MaxConnectionsPerServer > 0 {
		return NewConnectionLimiter(transport, conf.MaxConnectionsPerServer)
	}
	return transport
}

// New creates a new RoundTripper from the supplied upstream config
func New(conf *config.Upstream) (http.RoundTripper, error) {

	balancingAlgo, err := balancing.New(conf.Balancing)
	if err != nil {
		return nil, err
	}

	//!TODO: pass app cancel channel to the dns resolver
	initDNSResolver(balancingAlgo, conf.Addresses)

	return &Upstream{
		transport: getTransport(conf.Settings),
		getUpstreamAddress: func(uri string) *url.URL {
			return balancingAlgo.Get(uri).URL //!TODO: use IP:port, not the URL
		},
	}, nil
}

// NewSimple creates a simple RoundTripper with the default configuration that
// proxies requests to the supplied URL
func NewSimple(upstream *url.URL) http.RoundTripper {
	return &Upstream{
		transport: getTransport(config.GetDefaultUpstreamSettings()),
		getUpstreamAddress: func(_ string) *url.URL {
			return upstream // Always return the same single url - no balancing needed
		},
	}
}
