package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	// crawlRetryPause is knot2's CRAWL_RETRY_PAUSE.
	crawlRetryPause     = 30 * time.Second
	crawlRequestTimeout = 30 * time.Second
	listenProbePause    = 50 * time.Millisecond
)

// requestCrawls announces the knot to every crawler the way knot2 does: only
// once the listener accepts connections (a relay dials straight back), and
// per crawler until it accepts. It returns once the announcers are started;
// they stop when ctx is cancelled.
func requestCrawls(ctx context.Context, listenAddr string, crawlers []string, hostname string, retryPause time.Duration, logger *slog.Logger) {
	if len(crawlers) == 0 || !waitForListener(ctx, listenAddr) {
		return
	}
	for _, crawler := range crawlers {
		go requestCrawlUntilAccepted(ctx, crawler, hostname, retryPause, logger)
	}
}

// waitForListener blocks until listenAddr accepts a TCP connection, and
// reports false if ctx ends first.
func waitForListener(ctx context.Context, listenAddr string) bool {
	probe := probeAddr(listenAddr)
	var d net.Dialer
	for {
		if conn, err := d.DialContext(ctx, "tcp", probe); err == nil {
			conn.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(listenProbePause):
		}
	}
}

// probeAddr turns a wildcard listen address into one that can be dialed.
func probeAddr(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}
	switch ip := net.ParseIP(host); {
	case host == "":
		host = "localhost"
	case ip != nil && ip.IsUnspecified() && ip.To4() != nil:
		host = "127.0.0.1"
	case ip != nil && ip.IsUnspecified():
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}

// requestCrawlUntilAccepted retries on transport errors, 5xx and 429. Any
// other 4xx is the crawler declining, so it gives up on that crawler.
func requestCrawlUntilAccepted(ctx context.Context, crawler, hostname string, retryPause time.Duration, logger *slog.Logger) {
	url := strings.TrimSuffix(crawler, "/") + "/xrpc/com.atproto.sync.requestCrawl"
	body, _ := json.Marshal(map[string]string{"hostname": hostname})
	client := &http.Client{Timeout: crawlRequestTimeout}
	for {
		status, err := postCrawl(ctx, client, url, body)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Warn("requestCrawl failed", "crawler", url, "err", err)
		case status < 300:
			logger.Info("requested crawl", "crawler", url)
			return
		case status >= 400 && status < 500 && status != http.StatusTooManyRequests:
			logger.Error("requestCrawl rejected; giving up on this crawler", "crawler", url, "status", status)
			return
		default:
			logger.Warn("requestCrawl failed", "crawler", url, "status", status)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryPause):
		}
	}
}

func postCrawl(ctx context.Context, client *http.Client, url string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
