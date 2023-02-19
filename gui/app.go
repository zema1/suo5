package main

import (
	"context"
	"fmt"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zema1/suo5/ctrl"
	_ "net/http/pprof"
	"os"
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
				runtime.EventsEmit(a.ctx, "status", a.GetStatus())
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

func (a *App) RunSuo5WithConfig(config *ctrl.Suo5Config) {
	cliCtx, cancel := context.WithCancel(a.ctx)
	a.cancelSuo5 = cancel
	config.OnRemoteConnected = func(e *ctrl.ConnectedEvent) {
		runtime.EventsEmit(a.ctx, "connected", e)
	}
	config.OnNewClientConnection = func(event *ctrl.ClientConnectionEvent) {
		atomic.AddInt32(&a.connCount, 1)
	}
	config.OnClientConnectionClose = func(event *ctrl.ClientConnectCloseEvent) {
		atomic.AddInt32(&a.connCount, -1)
	}

	config.GuiLog = &GuiLogger{ctx: a.ctx}
	go func() {
		defer cancel()
		err := ctrl.Run(cliCtx, config)
		if err != nil {
			runtime.EventsEmit(a.ctx, "error", err.Error())
		}
	}()
}

func (a *App) DefaultSuo5Config() *ctrl.Suo5Config {
	return ctrl.DefaultSuo5Config()
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

	info, err := newProcess.MemoryInfo()
	if err != nil {
		return status
	}
	status.MemoryUsage = fmt.Sprintf("%.2fMB", float64(info.VMS)/1024/1024)
	return status
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
	runtime.EventsEmit(g.ctx, "log", string(p))
	return len(p), nil
}
