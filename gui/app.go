package main

import (
	"context"
	"encoding/json"
	"fmt"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zema1/suo5/ctrl"
	"github.com/zema1/suo5/suo5"
	"math"
	"net/url"
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
	speed     atomic.Pointer[suo5.SpeedStatisticEvent]
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// Startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) Startup(ctx context.Context) {
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

func (a *App) RunSuo5WithConfig(config *suo5.Suo5Config) {
	cliCtx, cancel := context.WithCancel(a.ctx)
	a.cancelSuo5 = cancel
	config.OnRemoteConnected = func(e *suo5.ConnectedEvent) {
		wailsRuntime.EventsEmit(a.ctx, "connected", e.Mode)
	}
	config.OnNewClientConnection = func(event *suo5.ClientConnectionEvent) {
		atomic.AddInt32(&a.connCount, 1)
	}
	config.OnClientConnectionClose = func(event *suo5.ClientConnectCloseEvent) {
		atomic.AddInt32(&a.connCount, -1)
	}
	config.OnSpeedInfo = func(event *suo5.SpeedStatisticEvent) {
		a.speed.Store(event)
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

func (a *App) GetStatus() *RunStatus {
	count := atomic.LoadInt32(&a.connCount)
	status := &RunStatus{
		ConnectionCount: count,
	}
	speedInfo := a.speed.Load()
	if speedInfo != nil {
		status.Upload = formatSpeed(float64(speedInfo.Upload))
		status.Download = formatSpeed(float64(speedInfo.Download))
	} else {
		status.Upload = "0b/s"
		status.Download = "0b/s"
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

func (a *App) Shutdown(_ context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
	if a.cancelSuo5 != nil {
		a.cancelSuo5()
	}
}

// Stop suo5,  for frontend use
func (a *App) Stop() {
	if a.cancelSuo5 != nil {
		a.cancelSuo5()
	}
	a.speed.Store(nil)
}

type RunStatus struct {
	ConnectionCount int32  `json:"connection_count"`
	Upload          string `json:"upload"`
	Download        string `json:"download"`
}

type GuiLogger struct {
	ctx context.Context
}

func (g *GuiLogger) Write(p []byte) (n int, err error) {
	wailsRuntime.EventsEmit(g.ctx, "log", string(p))
	return len(p), nil
}

// formatSpeed 将以字节/秒为单位的速率格式化为人类可读的字符串。
// 例如："2b/s", "320kb/s", "3Mb/s"。
// 这里 "b" 代表字节, "k" 代表 1000, "M" 代表 1000*1000。
func formatSpeed(bytesPerSecond float64) string {
	if bytesPerSecond < 0 {
		bytesPerSecond = 0 // 速度不应为负
	}

	// 定义单位和用户请求的后缀
	const unit = 1000.0
	val := bytesPerSecond
	suffix := "b/s" // 默认为 Bytes/second

	if val >= unit { // 大于或等于 1 KB/s
		val /= unit
		suffix = "kb/s"  // KiloBytes/second
		if val >= unit { // 大于或等于 1 MB/s (1000 KB/s)
			val /= unit
			suffix = "Mb/s"  // MegaBytes/second
			if val >= unit { // 大于或等于 1 GB/s (1000 MB/s)
				val /= unit
				suffix = "Gb/s"  // GigaBytes/second
				if val >= unit { // 大于或等于 1 TB/s (1000 GB/s)
					val /= unit
					suffix = "Tb/s" // TeraBytes/second
					// 如果需要，可以添加更多后缀 (Pb/s, Eb/s)
				}
			}
		}
	}

	// 格式化输出以匹配用户示例 "320kb/s", "3Mb/s", "2b/s"
	// 如果是整数或者是 "b/s" 单位，则显示为整数。
	// 否则 (对于 kb/s, Mb/s 等非整数值)，使用一位小数。
	if suffix == "b/s" {
		// 对于 "b/s"，总是四舍五入到最近的整数
		return fmt.Sprintf("%d%s", int64(math.Round(val)), suffix)
	}

	// 对于 kb/s, Mb/s 等
	if val == float64(int64(val)) { // 如果值是整数 (例如 320.0)
		return fmt.Sprintf("%d%s", int64(val), suffix)
	}
	return fmt.Sprintf("%.1f%s", val, suffix) // 例如 "320.5kb/s"
}
