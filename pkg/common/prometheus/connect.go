package prometheus

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/openshift/osde2e/pkg/common/config"
	"github.com/prometheus/client_golang/api"
)

// weatherRoundTripper is like api.DefaultRoundTripper with an added stripping of cert verification
// and adding the bearer token to the HTTP request
var weatherRoundTripper http.RoundTripper = &http.Transport{
	Proxy: func(request *http.Request) (*url.URL, error) {
		request.Header.Add("Authorization", "Bearer "+config.Instance.Prometheus.BearerToken)
		return http.ProxyFromEnvironment(request)
	},
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
	},
	TLSHandshakeTimeout: 10 * time.Second,
}

// CreateClient will create a Prometheus client based off of the global config.
func CreateClient() (api.Client, error) {
	return api.NewClient(api.Config{
		Address:      config.Instance.Prometheus.Address,
		RoundTripper: weatherRoundTripper,
	})
}
