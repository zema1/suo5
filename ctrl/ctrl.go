package ctrl

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/go-gost/gosocks5/server"
	log "github.com/kataras/golog"
	"github.com/kataras/pio"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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

	ignoreTls := &tls.Config{
		InsecureSkipVerify: true,
	}

	noTimeoutClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: ignoreTls,
		},
		Timeout: 0,
	}
	normalClient := &http.Client{
		Timeout: time.Duration(config.Timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: ignoreTls,
		},
	}
	rawClient := rawhttp.NewClient(&rawhttp.Options{
		Timeout:                0,
		FollowRedirects:        false,
		MaxRedirects:           0,
		AutomaticHostHeader:    true,
		AutomaticContentLength: true,
		ForceReadAllBody:       false,
	})

	baseHeader := http.Header{}
	baseHeader.Set("User-Agent", config.UserAgent)

	log.Infof("testing memory shell ...")
	err := checkMemshell(normalClient, config.Target, baseHeader.Clone())
	if err != nil {
		return err
	}
	log.Infof("memory shell basic check passed")
	if config.Mode == AutoDuplex || config.Mode == FullDuplex {
		log.Infof("checking the capability of FullDuplex..")
		if checkFullDuplex(config.Target, baseHeader.Clone()) {
			config.Mode = FullDuplex
			log.Infof("Wow, you can run the proxy on FullDuplex mode")
		} else {
			config.Mode = HalfDuplex
			log.Warnf("the target may behind reverse proxy, fallback to HalfDuplex mode")
		}
	}
	log.Infof("tunnel created at mode %s!", config.Mode)
	if config.OnRemoteConnected != nil {
		config.OnRemoteConnected(&ConnectedEvent{Mode: config.Mode})
	}

	fmt.Println()
	msg := "[Tunnel Info]\n"
	msg += fmt.Sprintf("Target:  %s\n", config.Target)
	msg += fmt.Sprintf("Proxy:   socks5://%s\n", config.Listen)
	if config.NoAuth {
		msg += fmt.Sprintf("Auth:    Not Set\n")
	} else {
		msg += fmt.Sprintf("Auth:    %s %s\n", config.Username, config.Password)
	}
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
		target:          config.Target,
		normalClient:    normalClient,
		noTimeoutClient: noTimeoutClient,
		rawClient:       rawClient,
		bufSize:         config.BufferSize,
		pool:            trPool,
		selector:        selector,
		mode:            config.Mode,
		baseHeader:      baseHeader,
	}
	_ = srv.Serve(&ClientEventHandler{
		Inner:                   handler,
		OnNewClientConnection:   config.OnNewClientConnection,
		OnClientConnectionClose: config.OnClientConnectionClose,
	})
	return nil
}

func checkMemshell(client *http.Client, target string, baseHeader http.Header) error {
	data := RandString(64)
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(data))
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

	if len(body) != 32 || !strings.HasPrefix(data, string(body)) {
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(body))
		return fmt.Errorf("got unexpected body, memshell test failed")
	}

	log.Infof("memshell connect success!!")
	return nil
}

func checkFullDuplex(target string, baseHeader http.Header) bool {
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
	req, err := http.NewRequest(http.MethodPost, target, netrans.NewChannelReader(ch))
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

	if len(body) != 32 || !strings.HasPrefix(data, string(body)) {
		return false
	}
	return true
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}
