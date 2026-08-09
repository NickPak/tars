package main

import (
	"embed"
	"log"

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
	application.RegisterEvent[StreamChunk]("agent:chunk")
	application.RegisterEvent[StreamDone]("agent:done")
	application.RegisterEvent[StreamError]("agent:error")
	application.RegisterEvent[ToolEvent]("agent:tool")
	application.RegisterEvent[ToolResultEvent]("agent:tool_result")
	application.RegisterEvent[ReasoningEvent]("agent:reasoning")
	application.RegisterEvent[ConversationRenamedEvent]("conversation:renamed")
	application.RegisterEvent[WorkspaceChangedEvent]("workspace:changed")
}

func main() {
	app := application.New(application.Options{
		Name:        "tars",
		Description: "Personal Agent",
		Icon:        iconPNG,
		Services: []application.Service{
			application.NewService(&AgentService{}),
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
