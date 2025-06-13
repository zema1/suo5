package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zema1/suo5/suo5"
	"os"
	"os/signal"
	"strings"

	// _ "github.com/chainreactors/proxyclient/extend"
	log "github.com/kataras/golog"
	"github.com/urfave/cli/v2"
	"github.com/zema1/suo5/ctrl"
)

var Version = "v0.0.0"

func main() {
	ctrl.InitDefaultLog(os.Stdout)
	app := cli.NewApp()
	app.Name = "suo5"
	app.Usage = "A high-performance http tunnel"
	app.Version = Version

	defaultConfig := suo5.DefaultSuo5Config()
	app.DisableSliceFlagSeparator = true

	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "the filepath for json config file",
			Value:   "",
		},
		&cli.StringFlag{
			Name:     "target",
			Aliases:  []string{"t"},
			Usage:    "the remote server url, ex: http://localhost:8080/suo5.jsp",
			Value:    defaultConfig.Target,
			Required: true,
		},
		&cli.StringFlag{
			Name:    "listen",
			Aliases: []string{"l"},
			Usage:   "listen address of socks5 server",
			Value:   defaultConfig.Listen,
		},
		&cli.StringFlag{
			Name:    "method",
			Aliases: []string{"m"},
			Usage:   "http request method",
			Value:   defaultConfig.Method,
		},
		&cli.StringFlag{
			Name:    "redirect",
			Aliases: []string{"r"},
			Usage:   "redirect to the url if host not matched, used to bypass load balance",
			Value:   defaultConfig.RedirectURL,
		},
		&cli.StringFlag{
			Name:  "auth",
			Usage: "socks5 creds, username:password, leave empty to auto generate",
			Value: "",
		},
		&cli.StringFlag{
			Name:  "mode",
			Usage: "connection mode, choices are auto, full, half, classic",
			Value: string(defaultConfig.Mode),
		},
		&cli.StringSliceFlag{
			Name:    "header",
			Aliases: []string{"H"},
			Usage:   "use extra header, ex -H 'Cookie: abc'",
			Value:   cli.NewStringSlice(defaultConfig.RawHeader...),
		},
		&cli.StringFlag{
			Name:  "ua",
			Usage: "shortcut to set the request User-Agent",
			Value: "",
		},
		&cli.IntFlag{
			Name:  "timeout",
			Usage: "request timeout in seconds",
			Value: defaultConfig.Timeout,
		},
		&cli.IntFlag{
			Name:  "buf-size",
			Usage: "request max body size",
			Value: defaultConfig.BufferSize,
		},
		&cli.IntFlag{
			Name:  "retry",
			Usage: "request retry",
			Value: defaultConfig.RetryCount,
		},
		&cli.IntFlag{
			Name:    "classic-poll-qps",
			Usage:   "request poll qps, only used in classic mode",
			Aliases: []string{"qps"},
			Value:   defaultConfig.ClassicPollQPS,
		},
		&cli.StringSliceFlag{
			Name:  "proxy",
			Usage: "set upstream proxy, support socks5/http(s), eg: socks5://127.0.0.1:7890",
			Value: cli.NewStringSlice(defaultConfig.UpstreamProxy...),
		},
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"d"},
			Usage:   "debug the traffic, print more details",
			Value:   defaultConfig.Debug,
		},
		&cli.BoolFlag{
			Name:    "no-heartbeat",
			Aliases: []string{"nh"},
			Usage:   "disable heartbeat to the remote server which will send data every 5s",
			Value:   defaultConfig.DisableHeartbeat,
		},
		&cli.BoolFlag{
			Name:    "no-gzip",
			Aliases: []string{"ng"},
			Usage:   "disable gzip compression, which will improve compatibility with some old servers",
			Value:   defaultConfig.DisableHeartbeat,
		},
		&cli.BoolFlag{
			Name:    "jar",
			Aliases: []string{"j"},
			Usage:   "enable cookiejar",
			Value:   defaultConfig.EnableCookieJar,
		},
		&cli.StringFlag{
			Name:    "test-exit",
			Aliases: []string{"T"},
			Usage:   "test a real connection, if success exit(0), else exit(1)",
			Hidden:  true,
		},
		&cli.StringSliceFlag{
			Name:    "exclude-domain",
			Aliases: []string{"E"},
			Usage:   "exclude certain domain name for proxy, ex -E 'portswigger.net'",
		},
		&cli.StringFlag{
			Name:    "exclude-domain-file",
			Aliases: []string{"ef"},
			Usage:   "exclude certain domains for proxy in a file, one domain per line",
		},
		&cli.StringFlag{
			Name:    "forward",
			Aliases: []string{"f"},
			Usage:   "forward target address, enable forward mode when specified",
			Value:   defaultConfig.ForwardTarget,
		},
	}
	app.Before = func(c *cli.Context) error {
		if c.Bool("debug") {
			log.SetLevel("debug")
		}
		return nil
	}
	app.Action = Action

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func Action(c *cli.Context) error {
	listen := c.String("listen")
	target := c.String("target")
	auth := c.String("auth")
	mode := suo5.ConnectionType(c.String("mode"))
	bufSize := c.Int("buf-size")
	timeout := c.Int("timeout")
	debug := c.Bool("debug")
	proxy := c.StringSlice("proxy")
	retryCount := c.Int("retry")
	method := c.String("method")
	redirect := c.String("redirect")
	ua := c.String("ua")
	header := c.StringSlice("header")
	noHeartbeat := c.Bool("no-heartbeat")
	noGzip := c.Bool("no-gzip")
	jar := c.Bool("jar")
	testExit := c.String("test-exit")
	exclude := c.StringSlice("exclude-domain")
	excludeFile := c.String("exclude-domain-file")
	classicQPS := c.Int("classic-poll-qps")
	forward := c.String("forward")
	configFile := c.String("config")

	if ua != "" {
		header = append(header, fmt.Sprintf("User-Agent: %s", ua))
	}

	var username, password string
	if auth != "" {
		parts := strings.Split(auth, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid socks credentials, expected username:password")
		}
		username = parts[0]
		password = parts[1]
	}

	if excludeFile != "" {
		data, err := os.ReadFile(excludeFile)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				exclude = append(exclude, line)
			}
		}
	}

	config := &suo5.Suo5Config{
		Listen:           listen,
		Target:           target,
		Username:         username,
		Password:         password,
		Mode:             mode,
		BufferSize:       bufSize,
		Timeout:          timeout,
		Debug:            debug,
		UpstreamProxy:    proxy,
		Method:           method,
		RedirectURL:      redirect,
		RawHeader:        header,
		DisableHeartbeat: noHeartbeat,
		DisableGzip:      noGzip,
		EnableCookieJar:  jar,
		ClassicPollQPS:   classicQPS,
		TestExit:         testExit,
		ExcludeDomain:    exclude,
		ForwardTarget:    forward,
		RetryCount:       retryCount,
	}

	if configFile != "" {
		log.Infof("loading config from %s", configFile)
		data, err := os.ReadFile(configFile)
		if err != nil {
			return err
		}
		err = json.Unmarshal(data, config)
		if err != nil {
			return err
		}
	}

	ctx, cancel := signalCtx()
	defer cancel()
	return ctrl.Run(ctx, config)
}

func signalCtx() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}
