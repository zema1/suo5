package ctrl

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/go-gost/gosocks5/server"
	log "github.com/kataras/golog"
	"github.com/kataras/pio"
	"github.com/pkg/errors"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func Run(ctx context.Context, config *Suo5Config) error {
	if config.GuiLog != nil {
		// 防止多次执行出错
		log.Default = log.New()
		log.Default.AddOutput(config.GuiLog)
	}
	if config.Debug {
		log.SetLevel("debug")
	}

	err := config.parseHeader()
	if err != nil {
		return err
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	if config.UpstreamProxy != "" {
		proxy := strings.TrimSpace(strings.ToLower(config.UpstreamProxy))
		if !strings.HasPrefix(proxy, "socks5") {
			return fmt.Errorf("only support socks5 proxy, eg: socks5://127.0.0.1:1080")
		}
		config.UpstreamProxy = proxy
		u, err := url.Parse(config.UpstreamProxy)
		if err != nil {
			return err
		}
		log.Infof("using upstream proxy %v", proxy)
		tr.Proxy = http.ProxyURL(u)
	}
	if config.RedirectURL != "" {
		_, err := url.Parse(config.RedirectURL)
		if err != nil {
			return fmt.Errorf("failed to parse redirect url, %s", err)
		}
		log.Infof("using redirect url %v", config.RedirectURL)
	}
	noTimeoutClient := &http.Client{
		Transport: tr,
		Timeout:   0,
	}
	normalClient := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: tr,
	}
	rawClient := rawhttp.NewClient(&rawhttp.Options{
		Proxy:                  config.UpstreamProxy,
		Timeout:                0,
		FollowRedirects:        false,
		MaxRedirects:           0,
		AutomaticHostHeader:    true,
		AutomaticContentLength: true,
		ForceReadAllBody:       false,
	})

	log.Infof("header: %s", config.headerString())
	log.Infof("method: %s", config.Method)

	log.Infof("testing connection with remote server")
	err = checkMemshell(normalClient, config.Method, config.Target, config.Header.Clone())
	if err != nil {
		return err
	}
	log.Infof("connection to remote server successful")
	if config.Mode == AutoDuplex || config.Mode == FullDuplex {
		log.Infof("checking the capability of FullDuplex..")
		if checkFullDuplex(config.Method, config.Target, config.Header.Clone()) {
			config.Mode = FullDuplex
			log.Infof("wow, you can run the proxy on FullDuplex mode")
		} else {
			config.Mode = HalfDuplex
			log.Warnf("the target may behind a reverse proxy, fallback to HalfDuplex mode")
		}
	}
	log.Infof("tunnel created at mode %s!", config.Mode)
	if config.OnRemoteConnected != nil {
		config.OnRemoteConnected(&ConnectedEvent{Mode: config.Mode})
	}

	fmt.Println()
	var socks5Addr string
	msg := "[Tunnel Info]\n"
	msg += fmt.Sprintf("Target:  %s\n", config.Target)
	if config.NoAuth {
		socks5Addr = fmt.Sprintf("socks5://%s", config.Listen)
	} else {
		socks5Addr = fmt.Sprintf("socks5://%s:%s@%s", config.Username, config.Password, config.Listen)
	}
	msg += fmt.Sprintf("Proxy:   %s\n", socks5Addr)
	msg += fmt.Sprintf("Mode:    %s\n", config.Mode)
	fmt.Println(pio.Rich(msg, pio.Green))

	lis, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return err
	}
	// context cancel, close the listen
	srv := &server.Server{
		Listener: lis,
	}
	go func() {
		<-ctx.Done()
		log.Infof("server stopped")
		_ = srv.Close()
	}()

	trPool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, config.BufferSize)
		},
	}
	selector := server.DefaultSelector
	if !config.NoAuth {
		selector = server.NewServerSelector([]*url.Userinfo{
			url.UserPassword(config.Username, config.Password),
		})
	}

	handler := &socks5Handler{
		ctx:             ctx,
		config:          config,
		normalClient:    normalClient,
		noTimeoutClient: noTimeoutClient,
		rawClient:       rawClient,
		pool:            trPool,
		selector:        selector,
	}

	go func() {
		_ = srv.Serve(&ClientEventHandler{
			Inner:                   handler,
			OnNewClientConnection:   config.OnNewClientConnection,
			OnClientConnectionClose: config.OnClientConnectionClose,
		})
	}()

	if config.TestExit != "" {
		time.Sleep(time.Second * 1)
		if err := testConnection(socks5Addr, config.TestExit, time.Second*15); err != nil {
			return errors.Wrap(err, "test connection failed")
		}
		// exit(0)
		return nil
	}
	<-ctx.Done()
	return nil
}

func checkMemshell(client *http.Client, method string, target string, baseHeader http.Header) error {
	data := RandString(64)
	req, err := http.NewRequest(method, target, strings.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid target url, %s", err)
	}
	req.Header = baseHeader.Clone()
	req.Header.Set("Content-Type", ContentTypeChecking)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to %s", target)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// 如果是数据末尾的换行，并不影响使用
	b := strings.TrimRight(string(body), "\r\n")
	if !strings.HasPrefix(data, b) {
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(body))
		return fmt.Errorf("got unexpected body, remote server test failed")
	}

	return nil
}

func checkFullDuplex(method string, target string, baseHeader http.Header) bool {
	// 这里的 client 需要定义 timeout，不要用外面没有 timeout 的 rawCient
	rawClient := rawhttp.NewClient(&rawhttp.Options{
		Timeout:                3 * time.Second,
		FollowRedirects:        false,
		MaxRedirects:           0,
		AutomaticHostHeader:    true,
		AutomaticContentLength: true,
		ForceReadAllBody:       false,
	})
	data := RandString(64)
	ch := make(chan []byte, 1)
	ch <- []byte(data)
	go func() {
		// timeout
		time.Sleep(time.Second * 5)
		close(ch)
	}()
	req, err := http.NewRequest(method, target, netrans.NewChannelReader(ch))
	if err != nil {
		return false
	}
	req.Header = baseHeader.Clone()
	req.Header.Set("Content-Type", ContentTypeChecking)
	resp, err := rawClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// 如果此时能立马读取到响应，说明请求没有被缓存, 那么就可以变成全双工的
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	b := strings.TrimRight(string(body), "\r\n")
	if !strings.HasPrefix(data, b) {
		return false
	}
	return true
}

// 检查代理是否真正有效, 只要能按有响应即可，无论目标是否能连通
func testConnection(socks5 string, remote string, timeout time.Duration) error {
	log.Infof("checking connection to %s using %s", remote, socks5)
	u, err := url.Parse(socks5)
	if err != nil {
		return err
	}
	client := http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
		},
	}
	req, err := http.NewRequest(http.MethodGet, remote, nil)
	if err != nil {
		return err
	}
	req.Close = true
	resp, err := client.Do(req)
	if err != nil {
		if os.IsTimeout(err) {
			return err
		}
		log.Infof("test connection got error, but it's ok, %s", err)
		return nil
	}
	defer resp.Body.Close()
	data, err := httputil.DumpResponse(resp, false)
	if err != nil {
		log.Debugf("test connection got error when read response,  %s, but it's ok", err)
		return nil
	}
	log.Debugf("test connection got response for %s (without body)\n%s", remote, string(data))
	return nil
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}
