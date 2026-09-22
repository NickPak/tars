package main

import (
	"embed"
	"log"
	goruntime "runtime"

	"tars/pkg/event"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var iconPNG []byte

func init() {
	// Register custom events with their payload types. The binding generator
	// picks these up and provides a strongly typed JS/TS API for them.
	// 载荷一律注册为指针类型：与各发射点（WailsSink/服务层）实际发射的
	// 类型一致，运行时校验精确匹配，边界零拷贝。
	application.RegisterEvent[*event.StreamChunk]("agent:chunk")
	application.RegisterEvent[*event.StreamDone]("agent:done")
	application.RegisterEvent[*event.StreamError]("agent:error")
	application.RegisterEvent[*event.ToolEvent]("agent:tool")
	application.RegisterEvent[*event.ToolResultEvent]("agent:tool_result")
	application.RegisterEvent[*event.ReasoningEvent]("agent:reasoning")
	application.RegisterEvent[*event.ApprovalEvent]("agent:approval")
	application.RegisterEvent[*event.SessionRenamedEvent]("session:renamed")
	application.RegisterEvent[*event.ProjectRenamedEvent]("project:renamed")
	application.RegisterEvent[*event.CompressionStartedEvent]("session:compression_started")
	application.RegisterEvent[*event.CompressionDoneEvent]("session:compression_done")
	application.RegisterEvent[*event.CompressionFailedEvent]("session:compression_failed")
	application.RegisterEvent[*ModelChangedEvent]("model:changed")
}

func main() {
	app := application.New(application.Options{
		Name:        "tars",
		Description: "Personal Agent",
		Icon:        iconPNG,
		// 注册顺序即启动顺序，关闭为其反序（Wails v3 API 契约）：
		// AgentService 首位——它持有应用生命周期（ServiceStartup 创建
		// boot.App 并登记 boot.SetCurrent，ServiceShutdown 最后执行）。
		Services: []application.Service{
			application.NewService(&AgentService{}),
			application.NewService(&AgentsMDService{}),
			application.NewService(&CanvasService{}),
			application.NewService(&ConfigService{}),
			application.NewService(&ExportService{}),
			application.NewService(&FileService{}),
			application.NewService(&MCPService{}),
			application.NewService(&MemoryService{}),
			application.NewService(&ModelService{}),
			application.NewService(&SkillService{}),
			application.NewService(&StatService{}),
			application.NewService(&WorkspaceService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "TARS",
		Width:  1280,
		Height: 800,
		// 窗口最小尺寸：防止三栏布局（侧边栏+聊天+工作区）被过度挤压
		MinWidth:  960,
		MinHeight: 600,
		// Windows 下无边框：自定义标题栏由前端 TopBar 承担（拖拽区
		// app-region: drag + 最小化/最大化/关闭按钮）。macOS 保持
		// HiddenInset 原生样式（红绿灯按钮由系统提供），不受影响。
		Frameless: goruntime.GOOS == "windows",
		Windows: application.WindowsWindow{
			// WebView2 原生非客户区命中测试：让 app-region: drag 的
			// 拖拽/双击最大化走系统级处理（老 WebView2 静默退化，
			// 由 --wails-draggable JS 拖拽兜底）。
			NonClientRegionSupport: true,
		},
		Linux: application.LinuxWindow{
			Icon: iconPNG,
		},
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(19, 19, 20),
		URL:              "/",
	})

	// Run the application. This blocks until the application has been exited.
	err := app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
