package qcdn

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

/* -------------------------------------------------------------------------------

使用样例：

proxy := NewQcdnProxy()
proxy.SetStrategy("https://example-302.com", &QcdnStrategy{
	Backup: "https://example-cdn.com", // 备份域名，主域名下载失败的时候启用
	Boot: "https://example-cdn.com", // 首开优化域名，在 MakeVodURL 传入非 0 的 bootLen 时启用
})

bootLen := 0 // 通过 Boot 域名先加载多少字节，传 0 表示不做首开优化
url := proxy.MakeVodURL("https://example-302.com/video.mp4", bootLen)
...

// ---------------------------------------------------------------------------- */

type QcdnProxy struct {
	proxyHost string
	server    *httptest.Server
	init      sync.Once

	strategies map[urlBase]*urlStrategy // urlBase => strategy
}

func NewQcdnProxy() *QcdnProxy {
	return &QcdnProxy{
		strategies: make(map[urlBase]*urlStrategy),
	}
}

func (p *QcdnProxy) Close() {
	if s := p.server; s != nil {
		p.server = nil
		s.Close()
	}
}

type QcdnStrategy struct {
	Backup string
	Boot   string
}

func (p *QcdnProxy) SetStrategy(urlBase_ string, s *QcdnStrategy) {
	urlBase := urlBaseOf(urlBase_)
	p.strategies[urlBase] = &urlStrategy{
		backup: urlBaseOf(s.Backup),
		boot:   urlBaseOf(s.Boot),
	}
}

func (p *QcdnProxy) MakeVodURL(urlVod string, bootLen int) string {
	url, err := url.Parse(urlVod)
	if err != nil {
		return urlVod
	}
	_, ok := p.strategies[urlBase{url.Scheme, url.Host}]
	if !ok {
		return urlVod // 这个 url 没有策略，认为不由我们 Proxy 管辖，返回原始 url
	}
	url.Path = makeProxyPath(url.Scheme, url.Host, url.Path)
	url.Scheme = "http"
	url.Host = p.getProxyHost()
	return url.String()
}

type urlBase struct {
	scheme, host string
}

func urlBaseOf(urlBase_ string) urlBase {
	url, err := url.Parse(urlBase_)
	if err != nil || url.Path != "" {
		panic("invalid urlBase")
	}
	return urlBase{url.Scheme, url.Host}
}

type urlStrategy struct {
	backup urlBase
	boot   urlBase
}

func (p *QcdnProxy) getProxyHost() string {
	p.init.Do(func() {
		p.server = httptest.NewUnstartedServer(http.HandlerFunc(p.handle))
		p.server.Start()
		p.proxyHost = strings.TrimPrefix(p.server.URL, "http://")
	})
	return p.proxyHost
}

func (p *QcdnProxy) handle(w http.ResponseWriter, req *http.Request) {
	url := req.URL
	scheme, host, path, ok := parseProxyPath(url.Path)
	if !ok {
		http.Error(w, "invalid proxy path", 500)
		return
	}
	url.Path = path
	url.Scheme = scheme
	url.Host = host
	req.Host = url.Host
	req.RequestURI = ""
	if serveRequest(w, req) {
		return
	}
	s, ok := p.strategies[urlBase{scheme, host}]
	if !ok {
		http.Error(w, "url strategy not found", 500)
		return
	}
	url.Scheme = s.backup.scheme
	url.Host = s.backup.host
	req.Host = url.Host
	if serveRequest(w, req) {
		return
	}
	http.Error(w, "both main and backup server fail", 500)
}

func makeProxyPath(scheme, host, path string) string {
	return "/" + scheme + "," + host + path
}

func parseProxyPath(proxyPath string) (scheme, host, path string, ok bool) {
	parts := strings.SplitN(proxyPath[1:], "/", 2)
	if len(parts) != 2 {
		return
	}
	schemeAndHost := strings.SplitN(parts[0], ",", 2)
	if len(schemeAndHost) != 2 {
		return
	}
	return schemeAndHost[0], schemeAndHost[1], "/" + parts[1], true
}

func serveRequest(w http.ResponseWriter, req *http.Request) bool {
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode/100 != 5 { // 5xx
			copyHeader(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return true
		}
	}
	return false
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// -------------------------------------------------------------------------------
