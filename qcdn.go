package qcdn

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

/* -------------------------------------------------------------------------------

使用样例：

proxy := NewQcdnProxy()
proxy.SetStrategy("https://example-qcdn.com", &QcdnStrategy{
	Backup: "https://example-cdn.com", // 备份域名，主域名下载失败的时候启用
	Boot: "https://example-cdn.com", // 首开优化域名，在 MakeVodURL 传入非 0 的 bootLen 时启用
})

bootLen := 0 // 通过 Boot 域名先加载多少字节，传 0 表示不做首开优化
url := proxy.MakeVodURL("https://example-qcdn.com/video.mp4", bootLen)
...

// ---------------------------------------------------------------------------- */

type QcdnProxy struct {
	proxyHost string
	server    *httptest.Server
	init      sync.Once

	strategies map[urlBase]*urlStrategy // urlBase => strategy

	mutex     sync.Mutex
	redirects map[resource]resource

	client http.Client
}

type QcdnConfig struct {
	Timeout int // in ms
}

func NewQcdnProxy(conf *QcdnConfig) *QcdnProxy {
	if conf == nil {
		conf = new(QcdnConfig)
	}
	return &QcdnProxy{
		redirects:  make(map[resource]resource),
		strategies: make(map[urlBase]*urlStrategy),
		client: http.Client{
			Transport: http.DefaultTransport,
			Timeout:   time.Duration(conf.Timeout) * time.Millisecond,
		},
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

type resource struct {
	urlBase
	path string
}

func (p *QcdnProxy) redirectOf(uri resource) (ret resource, ok bool) {
	p.mutex.Lock()
	ret, ok = p.redirects[uri]
	p.mutex.Unlock()
	return
}

func (p *QcdnProxy) setRedirect(uri, to resource) {
	log.Println("setRedirect:", uri, to)
	p.mutex.Lock()
	p.redirects[uri] = to
	p.mutex.Unlock()
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
	uri, ok := parseProxyPath(url.Path)
	if !ok {
		http.Error(w, "invalid proxy path", 500)
		return
	}
	if uriRedirect, ok := p.redirectOf(uri); ok { // 如果已经进行过 redirect，直接用缓存的 redirectUrl
		url.Path = uriRedirect.path
		url.Scheme = uriRedirect.scheme
		url.Host = uriRedirect.host
		req.Host = url.Host
		req.RequestURI = ""
		if serveRequest(p.client, w, req) {
			return
		}
	} else {
		url.Path = uri.path
		url.Scheme = uri.scheme
		url.Host = uri.host
		req.Host = url.Host
		req.RequestURI = ""
		var last *http.Request
		client := p.client
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			last = req
			return nil
		}
		if serveRequest(client, w, req) && last != nil { // 请求成功并且存在 redirect，缓存它
			lastURL := last.URL
			urlBase := urlBase{lastURL.Scheme, lastURL.Host}
			p.setRedirect(uri, resource{urlBase, lastURL.Path})
			return
		}
	}
	s, ok := p.strategies[uri.urlBase]
	if !ok {
		http.Error(w, "url strategy not found", 500)
		return
	}
	if url.Host != s.backup.host {
		url.Path = uri.path
		url.Scheme = s.backup.scheme
		url.Host = s.backup.host
		req.Host = url.Host
		if serveRequest(p.client, w, req) { // 后续这个资源都请求到 backup 域名
			p.setRedirect(uri, resource{s.backup, uri.path})
			return
		}
	}
	http.Error(w, "both main and backup server fail", 500)
}

func makeProxyPath(scheme, host, path string) string {
	return "/" + scheme + "," + host + path
}

func parseProxyPath(proxyPath string) (uri resource, ok bool) {
	parts := strings.SplitN(proxyPath[1:], "/", 2)
	if len(parts) != 2 {
		return
	}
	schemeAndHost := strings.SplitN(parts[0], ",", 2)
	if len(schemeAndHost) != 2 {
		return
	}
	urlBase := urlBase{schemeAndHost[0], schemeAndHost[1]}
	return resource{urlBase, "/" + parts[1]}, true
}

func serveRequest(client http.Client, w http.ResponseWriter, req *http.Request) bool {
	resp, err := client.Do(req)
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
