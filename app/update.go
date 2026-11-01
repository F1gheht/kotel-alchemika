// Updates: the app checks GitHub Releases for a newer version, downloads its installer and runs it silently.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	repoAPI       = "https://api.github.com/repos/F1gheht/kotel-alchemika/releases/latest"
	assetName     = "KotelAlchemika-Setup.exe"
	assetURLStart = "https://github.com/F1gheht/kotel-alchemika/releases/download/"
)

type updateInfo struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Newer   bool   `json:"newer"`
	Notes   string `json:"notes"`
	Page    string `json:"page"`
	url     string
	size    int64
}

var (
	updMu      sync.Mutex
	updCache   *updateInfo
	updCacheAt time.Time
	updBusy    bool
)

// parseVersion turns "v2.5.0" into [2 5 0]; anything unparsable becomes zeros.
func parseVersion(s string) [3]int {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(strings.TrimFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' }))
		v[i] = n
	}
	return v
}

func newerThan(a, b string) bool {
	x, y := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func checkUpdate(fresh bool) (*updateInfo, error) {
	updMu.Lock()
	if !fresh && updCache != nil && time.Since(updCacheAt) < 30*time.Minute {
		c := *updCache
		updMu.Unlock()
		return &c, nil
	}
	updMu.Unlock()

	api, prefix := repoAPI, assetURLStart
	if t := os.Getenv("KOTEL_TEST_UPDATE_API"); t != "" { // tests only
		api, prefix = t, "http://127.0.0.1"
	}
	req, _ := http.NewRequest("GET", api, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	info := &updateInfo{Current: appVersion, Latest: appVersion}
	if resp.StatusCode == 404 { // no releases yet
		return info, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub ответил %d", resp.StatusCode)
	}
	var rel struct {
		Tag     string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Draft   bool   `json:"draft"`
		Pre     bool   `json:"prerelease"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	info.Latest = strings.TrimPrefix(rel.Tag, "v")
	info.Notes = rel.Body
	info.Page = rel.HTMLURL
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, assetName) && strings.HasPrefix(a.URL, prefix) {
			info.url, info.size = a.URL, a.Size
		}
	}
	info.Newer = !rel.Draft && !rel.Pre && info.url != "" && newerThan(rel.Tag, appVersion)
	updMu.Lock()
	updCache, updCacheAt = info, time.Now()
	updMu.Unlock()
	c := *info
	return &c, nil
}

// progress of the running update, read by the page every few hundred milliseconds
type updProgress struct {
	State string `json:"state"` // idle | downloading | installing | error
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

var updProg = updProgress{State: "idle"}

func setProg(f func(p *updProgress)) { updMu.Lock(); f(&updProg); updMu.Unlock() }

type countingWriter struct {
	w io.Writer
}

func (c countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	setProg(func(p *updProgress) { p.Done += int64(n) })
	return n, err
}

// downloadUpdate saves the release installer to the temp folder and checks it looks like a Windows program.
func downloadUpdate(info *updateInfo) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, _ := http.NewRequest("GET", info.url, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub ответил %d", resp.StatusCode)
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("KotelAlchemika-Setup-%s.exe", info.Latest))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	total := info.size
	if total <= 0 {
		total = resp.ContentLength
	}
	setProg(func(p *updProgress) { p.Total = total; p.Done = 0 })
	n, err := io.Copy(countingWriter{f}, io.LimitReader(resp.Body, 200<<20))
	f.Close()
	if err != nil {
		os.Remove(path)
		return "", err
	}
	if info.size > 0 && n != info.size {
		os.Remove(path)
		return "", errors.New("файл скачался не полностью")
	}
	head := make([]byte, 2)
	if g, err := os.Open(path); err == nil {
		g.Read(head)
		g.Close()
	}
	if string(head) != "MZ" {
		os.Remove(path)
		return "", errors.New("скачанный файл не похож на установщик")
	}
	return path, nil
}

func registerUpdateRoutes(mux *http.ServeMux, onQuit func()) {
	mux.HandleFunc("/api/update/check", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		info, err := checkUpdate(r.URL.Query().Get("fresh") != "")
		if err != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(info)
	})
	mux.HandleFunc("/api/open-github", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		openURL("https://github.com/F1gheht/kotel-alchemika")
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/update/progress", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		updMu.Lock()
		p := updProg
		updMu.Unlock()
		json.NewEncoder(w).Encode(p)
	})
	// Starts the update and returns at once; the page follows it through /api/update/progress.
	mux.HandleFunc("/api/update/install", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		updMu.Lock()
		if updBusy {
			updMu.Unlock()
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]string{"error": "обновление уже идёт"})
			return
		}
		updBusy = true
		updProg = updProgress{State: "downloading"}
		updMu.Unlock()
		go func() {
			fail := func(msg string) {
				updMu.Lock()
				updBusy = false
				updProg = updProgress{State: "error", Error: msg}
				updMu.Unlock()
			}
			info, err := checkUpdate(true)
			if err != nil {
				fail(err.Error())
				return
			}
			if !info.Newer {
				fail("новой версии нет")
				return
			}
			path, err := downloadUpdate(info)
			if err != nil {
				fail(err.Error())
				return
			}
			setProg(func(p *updProgress) { p.State = "installing" })
			if err := runInstaller(path); err != nil {
				fail(err.Error())
				return
			}
			time.Sleep(1200 * time.Millisecond) // let the page show "installing"
			onQuit()
		}()
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]string{"ok": "1"})
	})
}
