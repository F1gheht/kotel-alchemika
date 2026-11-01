//go:build windows

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procMessageBox      = user32.NewProc("MessageBoxW")
	procMetrics         = user32.NewProc("GetSystemMetrics")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procIsIconic        = user32.NewProc("IsIconic")
	procUILang          = kernel32.NewProc("GetUserDefaultUILanguage")
	procSetDpiAwareCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetDpiForSystem = user32.NewProc("GetDpiForSystem")
)

const (
	mbOK        = 0x0
	mbYesNo     = 0x4
	mbIconInfo  = 0x40
	mbIconWarn  = 0x30
	mbIconQuest = 0x20
	idYes       = 6
	swRestore   = 9
)

// isRussian follows the language chosen in the app (or in the installer); the system language is the fallback.
func isRussian() bool {
	if b, err := os.ReadFile(filepath.Join(appDataDir(), "state.json")); err == nil {
		var st struct {
			Lang string `json:"lang"`
		}
		if json.Unmarshal(b, &st) == nil && (st.Lang == "ru" || st.Lang == "en") {
			return st.Lang == "ru"
		}
	}
	l, _, _ := procUILang.Call()
	return l&0x3ff == 0x19
}

func tr(ru, en string) string {
	if isRussian() {
		return ru
	}
	return en
}

func messageBox(text, title string, flags uintptr) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(title)
	r, _, _ := procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
	return int(r)
}

func appTitle() string { return tr("Котёл алхимика", "Alchemist's Cauldron") }

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--uninstall" {
		uninstall()
		return
	}
	dataDir = appDataDir()
	os.MkdirAll(dataDir, 0o755)
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old") // left over from an update
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// Already running: bring that window to the front and leave.
		c := http.Client{Timeout: 2 * time.Second}
		if resp, e := c.Get("http://" + listenAddr + "/api/focus"); e == nil {
			resp.Body.Close()
		}
		return
	}

	var w webview2.WebView
	focus := func() {
		if w == nil {
			return
		}
		w.Dispatch(func() {
			h := uintptr(w.Window())
			if r, _, _ := procIsIconic.Call(h); r != 0 {
				procShowWindow.Call(h, swRestore)
			}
			procSetForeground.Call(h)
		})
	}
	quit := func() {
		if w != nil {
			w.Terminate()
			return
		}
		os.Exit(0)
	}
	go http.Serve(ln, newMux(focus, quit))

	// Sharp text on high-DPI screens.
	if procSetDpiAwareCtx.Find() == nil {
		procSetDpiAwareCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	}

	sw, _, _ := procMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := procMetrics.Call(1) // SM_CYSCREEN
	width, height := uint(float64(sw)*0.82), uint(float64(sh)*0.85)
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi > 0 {
			if maxW := uint(1500 * dpi / 96); width > maxW {
				width = maxW
			}
		}
	}

	w = webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(dataDir, "WebView2"),
		WindowOptions: webview2.WindowOptions{
			Title:  appTitle(),
			Width:  width,
			Height: height,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		// WebView2 runtime missing: offer the browser instead of failing.
		r := messageBox(tr(
			"Для окна приложения нужен компонент Microsoft Edge WebView2, а он не найден.\n\nОткрыть калькулятор в обычном браузере?\n\nЧтобы работало окном, установи WebView2 Runtime с сайта Microsoft:\nhttps://go.microsoft.com/fwlink/p/?LinkId=2124703",
			"The app window needs Microsoft Edge WebView2, which was not found.\n\nOpen the calculator in your browser instead?\n\nTo use the app window, install WebView2 Runtime from Microsoft:\nhttps://go.microsoft.com/fwlink/p/?LinkId=2124703"),
			appTitle(), mbYesNo|mbIconWarn)
		if r != idYes {
			return
		}
		exec.Command("rundll32", "url.dll,FileProtocolHandler", "http://"+listenAddr+"/").Start()
		watchBrowserTab()
		return
	}
	defer w.Destroy()
	dpi := uintptr(96)
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d > 0 {
			dpi = d
		}
	}
	w.SetSize(int(900*dpi/96), int(600*dpi/96), webview2.HintMin)
	w.Navigate("http://" + listenAddr + "/")
	w.Run() // returns when the window is closed
}

/* ---------- uninstall ---------- */

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + appName

func shortcutPaths() []string {
	var out []string
	for _, f := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Programs, windows.FOLDERID_Desktop} {
		if p, err := windows.KnownFolderPath(f, 0); err == nil {
			out = append(out, filepath.Join(p, "Котёл алхимика.lnk"), filepath.Join(p, "Alchemist's Cauldron.lnk"))
		}
	}
	return out
}

func uninstall() {
	if messageBox(tr("Удалить «Котёл алхимика» с этого компьютера?", "Remove Alchemist's Cauldron from this computer?"), appTitle(), mbYesNo|mbIconQuest) != idYes {
		return
	}
	// Close a running copy first.
	c := http.Client{Timeout: 2 * time.Second}
	if resp, err := c.Post("http://"+listenAddr+"/api/quit", "text/plain", nil); err == nil {
		resp.Body.Close()
		time.Sleep(1500 * time.Millisecond)
	}
	keepSettings := messageBox(tr("Сохранить настройки и план крафта на случай повторной установки?", "Keep your settings and craft plan in case you reinstall?"), appTitle(), mbYesNo|mbIconQuest) == idYes

	for _, p := range shortcutPaths() {
		os.Remove(p)
	}
	registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	data := appDataDir()
	if keepSettings {
		os.RemoveAll(filepath.Join(data, "WebView2"))
		os.RemoveAll(filepath.Join(data, "icons"))
	} else {
		os.RemoveAll(data)
	}

	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), appName) {
		// The running exe cannot delete itself: let a hidden cmd remove the folder after we exit.
		cmd := exec.Command("cmd", "/c", "ping 127.0.0.1 -n 3 >nul & rmdir /s /q \""+dir+"\"")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		cmd.Start()
	}
	messageBox(tr("«Котёл алхимика» удалён.", "Alchemist's Cauldron has been removed."), appTitle(), mbOK|mbIconInfo)
}

// runInstaller starts the downloaded installer in silent mode; it waits for this app to close, updates it and opens it again.
func runInstaller(path string) error {
	cmd := exec.Command(path, "--silent", "--launch")
	return cmd.Start()
}

// openURL opens a web page in the default browser.
func openURL(u string) { exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start() }
