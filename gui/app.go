package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/shirou/gopsutil/v3/process"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zema1/suo5/ctrl"
	"github.com/zema1/suo5/suo5"
	_ "net/http/pprof"
	"net/url"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

// App struct
type App struct {
	ctx        context.Context
	cancel     func()
	cancelSuo5 func()

	connCount int32
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	a.ctx = ctx
	a.cancel = cancel
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				wailsRuntime.EventsEmit(a.ctx, "status", a.GetStatus())
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (a *App) shutdown(_ context.Context) {
	a.Stop()
	a.cancel()
}

func (a *App) RunSuo5WithConfig(config *suo5.Suo5Config) {
	cliCtx, cancel := context.WithCancel(a.ctx)
	a.cancelSuo5 = cancel
	config.OnRemoteConnected = func(e *suo5.ConnectedEvent) {
		wailsRuntime.EventsEmit(a.ctx, "connected", e)
	}
	config.OnNewClientConnection = func(event *suo5.ClientConnectionEvent) {
		atomic.AddInt32(&a.connCount, 1)
	}
	config.OnClientConnectionClose = func(event *suo5.ClientConnectCloseEvent) {
		atomic.AddInt32(&a.connCount, -1)
	}

	config.GuiLog = &GuiLogger{ctx: a.ctx}
	go func() {
		defer cancel()
		err := ctrl.Run(cliCtx, config)
		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "error", err.Error())
		}
	}()
}

func (a *App) DefaultSuo5Config() *suo5.Suo5Config {
	return suo5.DefaultSuo5Config()
}

func (a *App) GetStatus() *Status {
	count := atomic.LoadInt32(&a.connCount)
	status := &Status{
		ConnectionCount: count,
	}
	newProcess, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return status
	}
	cpuPercent, _ := newProcess.CPUPercent()
	status.CPUPercent = fmt.Sprintf("%.2f%%", cpuPercent)

	if runtime.GOOS != "darwin" {
		info, err := newProcess.MemoryInfo()
		if err != nil {
			return status
		}
		status.MemoryUsage = fmt.Sprintf("%.2fMB", float64(info.VMS)/1024/1024)
	} else {
		status.MemoryUsage = "unsupported"
	}
	return status
}

func (a *App) ImportConfig() (*suo5.Suo5Config, error) {
	options := wailsRuntime.OpenDialogOptions{
		DefaultDirectory: "",
		DefaultFilename:  "",
		Title:            "导入 Suo5 配置",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "json",
				Pattern:     "*.json",
			},
		},
	}
	filepath, err := wailsRuntime.OpenFileDialog(a.ctx, options)
	if err != nil {
		return nil, err
	}
	// user canceled
	if filepath == "" {
		return nil, nil
	}
	var config suo5.Suo5Config
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}
	if config.Listen == "" {
		return nil, fmt.Errorf("invalid config")
	}
	return &config, nil
}

func (a *App) ExportConfig(config *suo5.Suo5Config) error {
	filename := "suo5-config.json"
	if config.Target != "" {
		u, err := url.Parse(config.Target)
		if err == nil {
			filename = u.Hostname() + ".json"
		}
	}

	options := wailsRuntime.SaveDialogOptions{
		DefaultFilename: filename,
		Title:           "导出 Suo5 配置",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "json",
				Pattern:     "*.json",
			},
		},
	}
	filepath, err := wailsRuntime.SaveFileDialog(a.ctx, options)
	if err != nil {
		return err
	}
	if filepath == "" {
		return fmt.Errorf("user canceled")
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath, data, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (a *App) Stop() {
	if a.cancelSuo5 != nil {
		a.cancelSuo5()
	}
}

type Status struct {
	ConnectionCount int32  `json:"connection_count"`
	MemoryUsage     string `json:"memory_usage"`
	CPUPercent      string `json:"cpu_percent"`
}

type GuiLogger struct {
	ctx context.Context
}

func (g *GuiLogger) Write(p []byte) (n int, err error) {
	wailsRuntime.EventsEmit(g.ctx, "log", string(p))
	return len(p), nil
}
