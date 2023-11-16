package qcdn

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQcdn_MainOK(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		io.WriteString(w, req.URL.Path)
	}))
	defer echo.Close()
	log.Println("echo.URL:", echo.URL)

	proxy := NewQcdnProxy()
	defer proxy.Close()

	proxy.SetStrategy(echo.URL, &QcdnStrategy{
		Backup: "http://not-exist.com",
	})

	url := proxy.MakeVodURL(echo.URL+"/hello", 0)
	log.Println("proxy.MakeVodURL:", url)

	resp, err := http.Get(url)
	checkHttpResp(t, resp, err, 200, "/hello")
}

func TestQcdn_MainFail(t *testing.T) {
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "fail")
	}))
	defer fail.Close()
	log.Println("fail.URL:", fail.URL)

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		io.WriteString(w, "backup")
	}))
	defer backup.Close()
	log.Println("backup.URL:", backup.URL)

	proxy := NewQcdnProxy()
	defer proxy.Close()

	proxy.SetStrategy(fail.URL, &QcdnStrategy{
		Backup: backup.URL,
	})

	url := proxy.MakeVodURL(fail.URL+"/hello", 0)
	log.Println("proxy.MakeVodURL:", url)

	resp, err := http.Get(url)
	checkHttpResp(t, resp, err, 200, "backup")
}

func checkHttpResp(t *testing.T, resp *http.Response, err error, code int, body string) {
	if err != nil {
		t.Fatal("proxy.MakeVodURL resp:", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != code {
		t.Fatal("proxy.MakeVodURL code:", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != body {
		t.Fatal("proxy.MakeVodURL io.ReadAll:", err, string(b))
	}
}
