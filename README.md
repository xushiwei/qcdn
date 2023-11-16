qcdn simple proxy
===

[![Build Status](https://github.com/xushiwei/qcdn/actions/workflows/go.yml/badge.svg)](https://github.com/xushiwei/qcdn/actions/workflows/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/xushiwei/qcdn)](https://goreportcard.com/report/github.com/xushiwei/qcdn)
[![GoDoc](https://pkg.go.dev/badge/github.com/xushiwei/qcdn.svg)](https://pkg.go.dev/github.com/xushiwei/qcdn)

初始化：

```go
proxy := NewQcdnProxy()
proxy.SetStrategy("https://example-qcdn.com", &QcdnStrategy{
	Backup: "https://example-cdn.com", // 备份域名，主域名下载失败的时候启用
	Boot: "https://example-cdn.com", // 首开优化域名，在 MakeVodURL 传入非 0 的 bootLen 时启用
})
```

访问资源：

```go
bootLen := 0 // 通过 Boot 域名先加载多少字节，传 0 表示不做首开优化
url := proxy.MakeVodURL("https://example-qcdn.com/video.mp4", bootLen)
...
```
