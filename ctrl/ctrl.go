package ctrl

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-gost/gosocks5"
	"github.com/go-gost/gosocks5/client"
	"github.com/go-gost/gosocks5/server"
	log "github.com/kataras/golog"
	"github.com/kataras/pio"
	"github.com/pkg/errors"
	"github.com/zema1/suo5/suo5"
)

func InitDefaultLog(writer io.Writer) {
	// log.SetTimeFormat("01-02 15:04")
	log.SetTimeFormat("15:04")
	log.SetOutput(writer)

	supportColor := pio.SupportColors(writer)
	log.Handle(func(l *log.Log) bool {
		prefix := log.GetTextForLevel(l.Level, supportColor)
		var message string

		if len(l.Stacktrace) != 0 {
			s := l.Stacktrace[0]
			parts := strings.Split(s.Source, "/")
			source := parts[len(parts)-1]
			message = fmt.Sprintf("%s %s [%s] %s", prefix, l.FormatTime(), source, l.Message)
		} else {
			message = fmt.Sprintf("%s %s %s", prefix, l.FormatTime(), l.Message)
		}

		if l.NewLine {
			message += "\n"
		}

		output := l.Logger.GetLevelOutput(l.Level.String())
		_, err := output.Write([]byte(message))
		return err == nil
	})
}

func Run(ctx context.Context, config *suo5.Suo5Config) error {
	if config.GuiLog != nil {
		// 防止多次执行出错
		InitDefaultLog(config.GuiLog)
	}
	if config.Debug {
		log.SetLevel("debug")
	} else {
		log.SetLevel("info")
	}

	suo5Client, err := suo5.Connect(ctx, config)
	if err != nil {
		return err
	}
	log.Infof("starting tunnel at %s", config.Listen)

	if config.OnRemoteConnected != nil {
		config.OnRemoteConnected(&suo5.ConnectedEvent{Mode: config.Mode})
	}

	fmt.Println()
	var socks5Addr string
	msg := "[Tunnel Info]\n"
	msg += fmt.Sprintf("Target:  %s\n", config.Target)

	if config.ForwardTarget != "" {
		msg += fmt.Sprintf("Forward: %s\n", config.ForwardTarget)
		msg += fmt.Sprintf("Listen:  %s\n", config.Listen)
	} else {
		if config.NoAuth() {
			socks5Addr = fmt.Sprintf("socks5://%s", config.Listen)
		} else {
			socks5Addr = fmt.Sprintf("socks5://%s:%s@%s", config.Username, config.Password, config.Listen)
		}
		msg += fmt.Sprintf("Proxy:   %s\n", socks5Addr)
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
		log.Infof("socks5 server stopped")
		_ = srv.Close()
	}()

	var handler server.Handler
	if config.ForwardTarget != "" {
		// 使用 Forward 模式
		handler = &suo5.ClientEventHandler{
			Inner:                   NewForwardHandler(ctx, suo5Client),
			OnNewClientConnection:   config.OnNewClientConnection,
			OnClientConnectionClose: config.OnClientConnectionClose,
		}
		log.Infof("running in forward mode, forwarding all connections to %s", config.ForwardTarget)
	} else {
		handler = &suo5.ClientEventHandler{
			Inner:                   NewSocks5Handler(ctx, suo5Client),
			OnNewClientConnection:   config.OnNewClientConnection,
			OnClientConnectionClose: config.OnClientConnectionClose,
		}
	}

	go func() {
		_ = srv.Serve(handler)
	}()

	// 如果是 forward 模式，不需要测试 socks5 连接
	if config.ForwardTarget != "" {
		log.Infof("forward mode enabled, skipping socks5 test")
	} else {
		log.Infof("creating a test connection to the remote target")
		ok := testTunnel(config.Listen, config.Username, config.Password, time.Second*10)
		time.Sleep(time.Millisecond * 500)
		if !ok {
			log.Errorf("tunnel created, but failed to establish connection")
			return fmt.Errorf("suo5 can not work on this server")
		} else {
			log.Infof("congratulations! everything works fine")
		}

		if config.TestExit != "" {
			if err := testAndExit(socks5Addr, config.TestExit, time.Second*15); err != nil {
				return errors.Wrap(err, "test connection failed")
			}
			return nil
		}
	}

	suo5Client.Wait()
	log.Infof("all cleaned up, suo5 is going to exit")
	return nil
}

// 检查代理是否真正有效, 只要能按有响应即可，尝试连一下 server 的 LocalPort, 这里写 0，在 jsp 里有判断
func testTunnel(socks5, username, password string, timeout time.Duration) bool {
	addr, _ := gosocks5.NewAddr("127.0.0.1:0")
	options := []client.DialOption{client.TimeoutDialOption(timeout)}
	if username != "" && password != "" {
		options = append(options, client.SelectorDialOption(client.NewClientSelector(url.UserPassword(username, password))))
	}

	conn, err := client.Dial(socks5, options...)
	if err != nil {
		log.Error(err)
		return false
	}
	defer conn.Close()
	if err := gosocks5.NewRequest(gosocks5.CmdConnect, addr).Write(conn); err != nil {
		log.Error(err)
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	reply, err := gosocks5.ReadReply(conn)
	if err != nil {
		log.Error(err)
		return false
	}
	log.Debugf("recv socks5 reply: %d", reply.Rep)
	return reply.Rep == gosocks5.Succeeded || reply.Rep == gosocks5.ConnRefused
}

func testAndExit(socks5 string, remote string, timeout time.Duration) error {
	log.Infof("checking connection to %s using %s", remote, socks5)
	u, err := url.Parse(socks5)
	if err != nil {
		return err
	}
	httpClient := http.Client{
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
	ua := suo5.RandUserAgent()
	req.Header.Set("User-Agent", ua)
	for k, v := range suo5.GetBrowserHeaders(ua) {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
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
