// Command desktop is the Wails shell. It owns the window and supplies the two
// capabilities only a host can provide — an event channel to the running
// interface and native file pickers — then binds the shared application
// service. All behaviour lives in internal/appservice, which the Electron
// sidecar reuses unchanged. See docs/design/electron-migration.md.
package main

import (
	"context"
	"embed"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/chowyu12/aiclaw/internal/appservice"
)

//go:embed all:frontend/dist
var assets embed.FS

// wailsDialogs shows the native pickers through the Wails runtime, which needs
// the window's context.
type wailsDialogs struct{}

func (wailsDialogs) PickFiles(ctx context.Context, request appservice.FilePicker) ([]string, error) {
	filters := make([]wailsruntime.FileFilter, 0, len(request.Filters))
	for _, filter := range request.Filters {
		filters = append(filters, wailsruntime.FileFilter{
			DisplayName: filter.DisplayName, Pattern: filter.Pattern,
		})
	}
	return wailsruntime.OpenMultipleFilesDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: request.Title, Filters: filters,
	})
}

func (wailsDialogs) PickDirectory(ctx context.Context, title string) (string, error) {
	return wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{Title: title})
}

func main() {
	host := appservice.NewHost(appservice.Options{Dialogs: wailsDialogs{}})

	err := wails.Run(&options.App{
		Title:             "AIClaw",
		Width:             1280,
		Height:            820,
		MinWidth:          680,
		MinHeight:         540,
		HideWindowOnClose: runtime.GOOS == "darwin",
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		OnStartup: func(ctx context.Context) {
			// The event channel only exists once the window does.
			host.SetEmitter(func(name string, data ...any) {
				wailsruntime.EventsEmit(ctx, name, data...)
			})
			// A failed start is recorded on the service and surfaced through
			// Status(), so the window still opens and can explain itself.
			_ = host.Start(ctx)
		},
		OnShutdown: func(context.Context) { host.Stop() },
		Bind: []interface{}{
			// Only the service is bound: the host's lifecycle must not be
			// reachable from the renderer.
			host.Service(),
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
