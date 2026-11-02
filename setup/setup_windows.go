//go:build windows

// Установщик «Котёл алхимика»: ставит приложение в профиль пользователя (без прав администратора),
// создаёт ярлыки и регистрирует удаление в «Приложения и возможности».
package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed app.exe.gz
var appGz []byte

//go:embed page.html
var pageHTML string

const (
	appName      = "KotelAlchemika"
	appVersion   = "2.8.21"
	exeName      = "KotelAlchemika.exe"
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + appName
	lnkRu        = "Котёл алхимика.lnk"
	lnkEn        = "Alchemist's Cauldron.lnk"
)

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	kernel32       = windows.NewLazySystemDLL("kernel32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
	procUILang     = kernel32.NewProc("GetUserDefaultUILanguage")
	procDpiCtx     = user32.NewProc("SetProcessDpiAwarenessContext")
	procSystemDpi  = user32.NewProc("GetDpiForSystem")
)

// scaled converts a size in 96-DPI pixels to the screen's real pixels.
func scaled(px int) int {
	if procSystemDpi.Find() == nil {
		if dpi, _, _ := procSystemDpi.Call(); dpi > 0 {
			return px * int(dpi) / 96
		}
	}
	return px
}

// lang is the language picked in the installer: the app's saved language on an update, otherwise the system one.
var lang = defaultLang()

func systemLang() string {
	if l, _, _ := procUILang.Call(); l&0x3ff == 0x19 {
		return "ru"
	}
	return "en"
}

func appStateFile() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserCacheDir()
	}
	return filepath.Join(base, appName, "state.json")
}

func defaultLang() string {
	if b, err := os.ReadFile(appStateFile()); err == nil {
		var st map[string]any
		if json.Unmarshal(b, &st) == nil {
			if l, ok := st["lang"].(string); ok && (l == "ru" || l == "en") {
				return l
			}
		}
	}
	return systemLang()
}

// saveAppLang makes the app start in the language picked here, keeping the rest of its saved state.
func saveAppLang() {
	st := map[string]any{}
	if b, err := os.ReadFile(appStateFile()); err == nil {
		json.Unmarshal(b, &st)
	}
	st["lang"] = lang
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(appStateFile()), 0o755)
	os.WriteFile(appStateFile(), b, 0o644)
}

func isRussian() bool { return lang == "ru" }

func lnkName() string { return tr(lnkRu, lnkEn) }

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

func installDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserCacheDir()
	}
	return filepath.Join(base, "Programs", appName)
}

func targetExe() string { return filepath.Join(installDir(), exeName) }

func alreadyInstalled() bool { _, err := os.Stat(targetExe()); return err == nil }

// closeRunningApp asks a running copy to quit so its exe can be replaced.
func closeRunningApp() {
	c := http.Client{Timeout: 2 * time.Second}
	if resp, err := c.Post("http://127.0.0.1:47821/api/quit", "text/plain", nil); err == nil {
		resp.Body.Close()
		time.Sleep(1500 * time.Millisecond)
	}
}

func writeApp() error {
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	zr, err := gzip.NewReader(bytes.NewReader(appGz))
	if err != nil {
		return err
	}
	tmp := targetExe() + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, zr); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	exe := targetExe()
	if _, err := os.Stat(exe); err == nil {
		os.Remove(exe + ".old")
		// A running exe can still be renamed, so an update never fails on a locked file.
		if err := os.Rename(exe, exe+".old"); err != nil {
			os.Remove(tmp)
			return errors.New(tr("не получилось заменить старую версию — закрой «Котёл алхимика» и попробуй снова", "could not replace the old version — close the app and try again"))
		}
	}
	return os.Rename(tmp, exe)
}

func makeShortcut(lnk string) error {
	ps := `$ws=New-Object -ComObject WScript.Shell; $s=$ws.CreateShortcut($env:K_LNK); $s.TargetPath=$env:K_EXE; $s.WorkingDirectory=$env:K_DIR; $s.IconLocation=$env:K_EXE+',0'; $s.Description=$env:K_DESC; $s.Save()`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", ps)
	cmd.Env = append(os.Environ(), "K_LNK="+lnk, "K_EXE="+targetExe(), "K_DIR="+installDir(), "K_DESC="+tr("Калькулятор крафта и перекрафта Albion Online", "Albion Online crafting and refining calculator"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run()
}

func registerUninstall() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	exe := targetExe()
	size := uint32(0)
	if fi, err := os.Stat(exe); err == nil {
		size = uint32(fi.Size() / 1024)
	}
	k.SetStringValue("DisplayName", tr("Котёл алхимика", "Alchemist's Cauldron"))
	k.SetStringValue("DisplayVersion", appVersion)
	k.SetStringValue("Publisher", "Kotel Alchemika")
	k.SetStringValue("DisplayIcon", exe+",0")
	k.SetStringValue("InstallLocation", installDir())
	k.SetStringValue("UninstallString", `"`+exe+`" --uninstall`)
	k.SetDWordValue("NoModify", 1)
	k.SetDWordValue("NoRepair", 1)
	k.SetDWordValue("EstimatedSize", size)
	return nil
}

type options struct {
	Desktop bool   `json:"desktop"`
	Launch  bool   `json:"launch"`
	Lang    string `json:"lang"`
}

func install(o options) (warn string, err error) {
	if o.Lang == "ru" || o.Lang == "en" {
		lang = o.Lang
	}
	closeRunningApp()
	if err := writeApp(); err != nil {
		return "", err
	}
	saveAppLang()
	var failed []string
	// Shortcuts carry the name in the chosen language; drop ones left in the other language.
	for _, f := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Programs, windows.FOLDERID_Desktop} {
		if p, e := windows.KnownFolderPath(f, 0); e == nil {
			os.Remove(filepath.Join(p, tr(lnkEn, lnkRu)))
		}
	}
	if p, e := windows.KnownFolderPath(windows.FOLDERID_Programs, 0); e == nil {
		if makeShortcut(filepath.Join(p, lnkName())) != nil {
			failed = append(failed, tr("в меню «Пуск»", "in the Start menu"))
		}
	}
	if o.Desktop {
		if p, e := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0); e == nil {
			if makeShortcut(filepath.Join(p, lnkName())) != nil {
				failed = append(failed, tr("на рабочем столе", "on the desktop"))
			}
		}
	}
	if err := registerUninstall(); err != nil {
		failed = append(failed, tr("в списке «Приложения»", "in Apps & features"))
	}
	if len(failed) > 0 {
		warn = tr("Приложение установлено, но не получилось создать ярлык или запись ", "The app is installed, but a shortcut or entry could not be created ")
		for i, f := range failed {
			if i > 0 {
				warn += ", "
			}
			warn += f
		}
		warn += "."
	}
	return warn, nil
}

func launch() {
	exec.Command(targetExe()).Start()
}

// relaunch starts the updated app; the flag tells it to wait for the old copy to finish closing.
func relaunch() {
	exec.Command(targetExe(), "--after-update").Start()
}

// waitForExit waits up to 20 seconds for the old app process to end.
func waitForExit(pid int) {
	if pid <= 0 {
		return
	}
	const synchronize = 0x00100000
	h, err := windows.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	windows.WaitForSingleObject(h, 20000)
}

// waitPortFree waits until nothing listens on the app's address (the old copy has let it go).
func waitPortFree() {
	for i := 0; i < 40; i++ {
		c, err := net.DialTimeout("tcp", "127.0.0.1:47821", 300*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(500 * time.Millisecond)
	}
}

/* ---------- UI ---------- */

type status struct {
	State string `json:"state"` // idle | working | done | error
	Msg   string `json:"msg"`
}

// silentUpdate is run by the app's built-in updater: no window, keeps the existing shortcuts and language.
func silentUpdate(launchAfter bool, waitPid int) {
	desktop := false
	if p, e := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0); e == nil {
		for _, n := range []string{lnkRu, lnkEn} {
			if _, err := os.Stat(filepath.Join(p, n)); err == nil {
				desktop = true
			}
		}
	}
	// Let the old app close completely before replacing it.
	waitForExit(waitPid)
	waitPortFree()
	if _, err := install(options{Desktop: desktop, Lang: lang}); err != nil {
		messageBox(tr("Не получилось обновить «Котёл алхимика»: ", "Could not update Alchemist's Cauldron: ")+err.Error(), tr("Обновление", "Update"), 0x10)
		return
	}
	if launchAfter {
		waitPortFree()
		relaunch()
	}
}

func main() {
	if procDpiCtx.Find() == nil {
		procDpiCtx.Call(^uintptr(3))
	}
	silent, launchAfter, waitPid := false, false, 0
	for i, a := range os.Args[1:] {
		switch a {
		case "--silent":
			silent = true
		case "--launch":
			launchAfter = true
		case "--wait-pid":
			if i+2 < len(os.Args) {
				waitPid, _ = strconv.Atoi(os.Args[i+2])
			}
		}
	}
	if silent {
		silentUpdate(launchAfter, waitPid)
		return
	}
	title := tr("Установка «Котёл алхимика»", "Alchemist's Cauldron Setup")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus:     true,
		DataPath:      filepath.Join(os.TempDir(), appName+"-setup-webview"),
		WindowOptions: webview2.WindowOptions{Title: title, Width: uint(scaled(620)), Height: uint(scaled(520)), IconId: 1, Center: true},
	})
	if w == nil {
		fallbackInstall(title)
		return
	}
	defer w.Destroy()

	var mu sync.Mutex
	st := status{State: "idle"}
	w.Bind("setupInfo", func() map[string]any {
		return map[string]any{"dir": installDir(), "lang": lang, "update": alreadyInstalled(), "version": appVersion}
	})
	w.Bind("setupSetLang", func(l string) {
		if l == "ru" || l == "en" {
			lang = l
			w.SetTitle(tr("Установка «Котёл алхимика»", "Alchemist's Cauldron Setup"))
		}
	})
	w.Bind("setupInstall", func(o options) {
		mu.Lock()
		if st.State == "working" {
			mu.Unlock()
			return
		}
		st = status{State: "working"}
		mu.Unlock()
		go func() {
			warn, err := install(o)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				st = status{State: "error", Msg: err.Error()}
				return
			}
			st = status{State: "done", Msg: warn}
			if o.Launch {
				launch()
			}
		}()
	})
	w.Bind("setupStatus", func() status { mu.Lock(); defer mu.Unlock(); return st })
	w.Bind("setupLaunch", func() { launch(); w.Terminate() })
	w.Bind("setupClose", func() { w.Terminate() })
	w.SetSize(scaled(620), scaled(520), webview2.HintFixed)
	w.SetHtml(pageHTML)
	w.Run()
}

// fallbackInstall is used when WebView2 is missing: the same install with plain Windows dialogs.
func fallbackInstall(title string) {
	const yesNo, iconQ, iconI, iconE, idYes = 0x4, 0x20, 0x40, 0x10, 6
	if messageBox(tr("Установить «Котёл алхимика» в\n", "Install Alchemist's Cauldron to\n")+installDir()+tr("\n\nЯрлыки появятся в меню «Пуск» и на рабочем столе.", "\n\nShortcuts will be added to the Start menu and the desktop."), title, yesNo|iconQ) != idYes {
		return
	}
	warn, err := install(options{Desktop: true, Lang: lang})
	if err != nil {
		messageBox(tr("Установка не удалась: ", "Setup failed: ")+err.Error(), title, iconE)
		return
	}
	msg := tr("Готово. Запустить сейчас?", "Done. Launch it now?")
	if warn != "" {
		msg = warn + "\n\n" + msg
	}
	if messageBox(msg, title, yesNo|iconI) == idYes {
		launch()
	}
}
