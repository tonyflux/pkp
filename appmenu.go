package main

import (
	"fmt"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/i18n"
)

// eventMenuCommand hands the page a menu item's command (frontend
// shortcuts.ts runCommand), the same one its key gives.
const eventMenuCommand = "menu:command"

// attachAppMenu replaces Wails' default macOS menu bar, which is English,
// reloads the whole page on ⌘R (losing its state) and opens wails.io in
// PKP's window from Help. PKP's is in UI Language and redrawn on a switch.
// Elsewhere the window has no menu bar; the keys alone do the work there.
// show brings the main window back first: it may be closed to the menu bar.
func attachAppMenu(app *application.App, show func()) {
	if runtime.GOOS != "darwin" {
		return
	}
	set := func() { app.Menu.SetApplicationMenu(appMenu(app, show)) }
	set()
	i18n.OnChange(set)
}

func appMenu(app *application.App, show func()) *application.Menu {
	command := func(m *application.Menu, label, accelerator, name string) {
		m.Add(label).SetAccelerator(accelerator).OnClick(func(*application.Context) {
			show()
			app.Event.Emit(eventMenuCommand, name)
		})
	}
	// The system's own items (the responder chain's copy:, hide:, …), in our words.
	role := func(m *application.Menu, r application.Role, label string) {
		m.AddRole(r)
		m.FindByRole(r).SetLabel(label)
	}

	root := application.NewMenu()

	pkp := root.AddSubmenu("PKP")
	role(pkp, application.About, i18n.T("关于 PKP"))
	command(pkp, i18n.T("检查更新…"), "", "updates")
	pkp.AddSeparator()
	command(pkp, i18n.T("设置…"), "CmdOrCtrl+,", "settings")
	pkp.AddSeparator()
	role(pkp, application.ServicesMenu, i18n.T("服务"))
	pkp.AddSeparator()
	role(pkp, application.Hide, i18n.T("隐藏 PKP"))
	role(pkp, application.HideOthers, i18n.T("隐藏其他"))
	role(pkp, application.ShowAll, i18n.T("全部显示"))
	pkp.AddSeparator()
	role(pkp, application.Quit, i18n.T("退出 PKP"))

	file := root.AddSubmenu(i18n.T("menu|文件"))
	command(file, i18n.T("云添加…"), "CmdOrCtrl+n", "cloudAdd")
	command(file, i18n.T("新建文件夹…"), "CmdOrCtrl+Shift+n", "newFolder")
	command(file, i18n.T("上传文件…"), "CmdOrCtrl+u", "upload")
	command(file, i18n.T("上传文件夹…"), "CmdOrCtrl+Shift+u", "uploadFolder")
	file.AddSeparator()
	role(file, application.CloseWindow, i18n.T("关闭窗口"))

	edit := root.AddSubmenu(i18n.T("编辑"))
	role(edit, application.Undo, i18n.T("撤销"))
	role(edit, application.Redo, i18n.T("重做"))
	edit.AddSeparator()
	role(edit, application.Cut, i18n.T("剪切"))
	role(edit, application.Copy, i18n.T("拷贝"))
	role(edit, application.Paste, i18n.T("粘贴"))
	role(edit, application.SelectAll, i18n.T("全选"))
	edit.AddSeparator()
	command(edit, i18n.T("搜索"), "CmdOrCtrl+f", "search")

	view := root.AddSubmenu(i18n.T("显示"))
	command(view, i18n.T("刷新"), "CmdOrCtrl+r", "refresh")
	view.AddSeparator()
	command(view, i18n.T("切换侧栏"), "CmdOrCtrl+\\", "toggleNav")
	command(view, i18n.T("查看信息"), "CmdOrCtrl+i", "info")
	view.AddSeparator()
	role(view, application.ResetZoom, i18n.T("实际大小"))
	role(view, application.ZoomIn, i18n.T("放大"))
	role(view, application.ZoomOut, i18n.T("缩小"))
	view.AddSeparator()
	role(view, application.ToggleFullscreen, i18n.T("切换全屏幕"))

	goTo := root.AddSubmenu(i18n.T("前往"))
	command(goTo, i18n.T("返回上一级"), "CmdOrCtrl+[", "back")
	goTo.AddSeparator()
	// Existing number shortcuts stay stable as navigation grows.
	for i, n := range []struct{ key, label string }{
		{"all", i18n.T("全部文件")},
		{"starred", i18n.T("星标")},
		{"cleanup", i18n.T("清理")},
		{"assistant", i18n.T("AI 助手")},
		{"history", i18n.T("播放历史")},
		{"transfers", i18n.T("传输中心")},
		{"sync", i18n.T("同步")},
		{"shares", i18n.T("我的分享")},
		{"trash", i18n.T("回收站")},
	} {
		command(goTo, n.label, fmt.Sprintf("CmdOrCtrl+%d", i+1), "nav:"+n.key)
	}

	window := root.AddSubmenu(i18n.T("窗口"))
	role(window, application.Minimise, i18n.T("最小化"))
	role(window, application.Zoom, i18n.T("缩放"))
	window.AddSeparator()
	role(window, application.BringAllToFront, i18n.T("前置全部窗口"))

	help := root.AddSubmenu(i18n.T("帮助"))
	command(help, i18n.T("键盘快捷键"), "CmdOrCtrl+/", "shortcuts")

	return root
}
