// Котёл алхимика — desktop calculator for Albion Online potion crafting and refining.
// This file holds the local HTTP server: the page, market prices, item icons and saved settings.
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

//go:embed ids.txt
var idsTxt string

const (
	appName    = "KotelAlchemika"
	appVersion = "2.9.3"
	listenAddr = "127.0.0.1:47821"
)

var cities = []string{"Brecilien", "Bridgewatch", "Martlock", "Thetford", "Fort Sterling", "Lymhurst", "Caerleon"}

var priceHosts = map[string]string{
	"europe": "https://europe.albion-online-data.com",
	"west":   "https://west.albion-online-data.com",
	"east":   "https://east.albion-online-data.com",
}

type cell struct {
	S  int    `json:"s,omitempty"`  // lowest sell order now
	B  int    `json:"b,omitempty"`  // highest buy order now
	A  int    `json:"a,omitempty"`  // 7-day average deal price
	V  int    `json:"v,omitempty"`  // average amount sold per day
	L  int    `json:"l,omitempty"`  // price of the last recorded sale (daily average of the last day with sales)
	LT string `json:"lt,omitempty"` // day of that sale
}

type priceRow struct {
	ItemID       string `json:"item_id"`
	City         string `json:"city"`
	SellPriceMin int    `json:"sell_price_min"`
	BuyPriceMax  int    `json:"buy_price_max"`
}

type histRow struct {
	ItemID   string `json:"item_id"`
	Location string `json:"location"`
	Data     []struct {
		ItemCount int     `json:"item_count"`
		AvgPrice  float64 `json:"avg_price"`
		Timestamp string  `json:"timestamp"`
	} `json:"data"`
}

type histEntry struct {
	at   time.Time
	data map[string]map[string]cell
}

type priceResult struct {
	At      string                     `json:"at"`
	Prices  map[string]map[string]cell `json:"prices"`
	Warning string                     `json:"warning,omitempty"`
}

var (
	httpClient = &http.Client{Timeout: 30 * time.Second}
	mu         sync.Mutex
	priceCache = map[string]*priceResult{}
	priceAt    = map[string]time.Time{}
	histCache  = map[string]*histEntry{}
	lastPing   = time.Now()
	pinged     bool
	byeAt      time.Time
	dataDir    string
	testHost   = os.Getenv("KOTEL_TEST_HOST")
)

func getJSON(u string, v any) error {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion+" (personal crafting calculator)")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("нет связи с сайтом цен — проверь интернет")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return errors.New("сайт с ценами просит подождать (слишком много запросов)")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("сайт с ценами ответил %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func itemIDs() []string { return strings.Split(strings.TrimSpace(idsTxt), ",") }

func locs() string { return url.QueryEscape(strings.Join(cities, ",")) }

func chunks(all []string, n int) [][]string {
	var out [][]string
	for i := 0; i < len(all); i += n {
		j := min(i+n, len(all))
		out = append(out, all[i:j])
	}
	return out
}

func fetchCurrent(host string) (map[string]map[string]cell, error) {
	return fetchCurrentIDs(host, itemIDs())
}

func fetchCurrentIDs(host string, ids []string) (map[string]map[string]cell, error) {
	out := map[string]map[string]cell{}
	for _, part := range chunks(ids, 100) {
		var rows []priceRow
		u := fmt.Sprintf("%s/api/v2/stats/prices/%s.json?locations=%s&qualities=1", host, strings.Join(part, ","), locs())
		if err := getJSON(u, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			if out[r.ItemID] == nil {
				out[r.ItemID] = map[string]cell{}
			}
			c := cell{}
			if r.SellPriceMin > 0 && r.SellPriceMin < 50_000_000 {
				c.S = r.SellPriceMin
			}
			if r.BuyPriceMax > 0 && r.BuyPriceMax < 50_000_000 {
				c.B = r.BuyPriceMax
			}
			out[r.ItemID][r.City] = c
		}
	}
	return out, nil
}

func fetchHistory(host string) (map[string]map[string]cell, error) {
	return fetchHistoryIDs(host, itemIDs(), 7, "1")
}

// fetchHistoryIDs loads daily sales for the last `days` days: the 7-day average and volume come from
// the last week, the last sale from the newest day that had any sales. Sales of the given qualities are
// added together (equipment sells in several qualities; most trading is in normal, good and outstanding).
func fetchHistoryIDs(host string, ids []string, days int, qualities string) (map[string]map[string]cell, error) {
	date := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	week := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	type agg struct{ cnt, sum float64 }
	byDay := map[string]map[string]map[string]*agg{} // item -> city -> day
	for i, part := range chunks(ids, 30) {
		if i > 0 {
			time.Sleep(350 * time.Millisecond) // stay well under the site's request limit
		}
		var rows []histRow
		u := fmt.Sprintf("%s/api/v2/stats/history/%s.json?locations=%s&qualities=%s&time-scale=24&date=%s", host, strings.Join(part, ","), locs(), qualities, date)
		if err := getJSON(u, &rows); err != nil {
			return nil, err
		}
		for _, h := range rows {
			if byDay[h.ItemID] == nil {
				byDay[h.ItemID] = map[string]map[string]*agg{}
			}
			if byDay[h.ItemID][h.Location] == nil {
				byDay[h.ItemID][h.Location] = map[string]*agg{}
			}
			days := byDay[h.ItemID][h.Location]
			for _, p := range h.Data {
				if p.ItemCount <= 0 || p.AvgPrice <= 0 {
					continue
				}
				d := days[p.Timestamp]
				if d == nil {
					d = &agg{}
					days[p.Timestamp] = d
				}
				d.cnt += float64(p.ItemCount)
				d.sum += float64(p.ItemCount) * p.AvgPrice
			}
		}
	}
	out := map[string]map[string]cell{}
	for id, cs := range byDay {
		out[id] = map[string]cell{}
		for city, ds := range cs {
			var cnt, sum float64
			last := ""
			for day, d := range ds {
				if day > last {
					last = day
				}
				if day != "" && day < week {
					continue
				}
				cnt += d.cnt
				sum += d.sum
			}
			c := cell{V: int(cnt/7 + 0.5)}
			if l := ds[last]; l != nil && l.cnt > 0 {
				c.L, c.LT = int(l.sum/l.cnt+0.5), last
			}
			if cnt > 0 {
				c.A = int(sum/cnt + 0.5)
			}
			out[id][city] = c
		}
	}
	return out, nil
}

func loadPrices(server string) (*priceResult, error) {
	host, ok := priceHosts[server]
	if testHost != "" {
		host, ok = testHost, true
	}
	if !ok {
		return nil, errors.New("неизвестный сервер")
	}
	mu.Lock()
	if r := priceCache[server]; r != nil && time.Since(priceAt[server]) < 2*time.Minute {
		mu.Unlock()
		return r, nil
	}
	he := histCache[server]
	mu.Unlock()

	cur, err := fetchCurrent(host)
	if err != nil {
		return nil, err
	}
	warning := ""
	if he == nil || time.Since(he.at) > time.Hour {
		h, herr := fetchHistory(host)
		if herr != nil {
			warning = "Текущие цены обновлены, а средние за 7 дней загрузить не удалось: " + herr.Error()
		} else {
			he = &histEntry{at: time.Now(), data: h}
			mu.Lock()
			histCache[server] = he
			mu.Unlock()
		}
	}
	if he != nil {
		for id, cs := range he.data {
			for city, hc := range cs {
				if cur[id] == nil {
					cur[id] = map[string]cell{}
				}
				c := cur[id][city]
				c.A, c.V, c.L, c.LT = hc.A, hc.V, hc.L, hc.LT
				cur[id][city] = c
			}
		}
	}
	r := &priceResult{At: time.Now().UTC().Format(time.RFC3339), Prices: cur, Warning: warning}
	mu.Lock()
	priceCache[server], priceAt[server] = r, time.Now()
	mu.Unlock()
	return r, nil
}

var iconID = regexp.MustCompile(`^[A-Z0-9_]{3,80}(@\d)?$`)

// Equipment prices are loaded on demand for the item the page shows: current prices every time,
// 7-day history at most once an hour per item.
type extraHist struct {
	at   time.Time
	data map[string]cell
}

var extraHistCache = map[string]map[string]extraHist{} // server -> item -> history

func loadExtra(server string, ids []string) (map[string]map[string]cell, error) {
	host, ok := priceHosts[server]
	if testHost != "" {
		host, ok = testHost, true
	}
	if !ok {
		return nil, errors.New("неизвестный сервер")
	}
	cur, err := fetchCurrentIDs(host, ids)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	hc := extraHistCache[server]
	if hc == nil {
		hc = map[string]extraHist{}
		extraHistCache[server] = hc
	}
	var stale []string
	for _, id := range ids {
		if h, ok := hc[id]; !ok || time.Since(h.at) > time.Hour {
			stale = append(stale, id)
		}
	}
	mu.Unlock()
	if len(stale) > 0 {
		if h, herr := fetchHistoryIDs(host, stale, 30, "1,2,3"); herr == nil {
			mu.Lock()
			for _, id := range stale {
				hc[id] = extraHist{at: time.Now(), data: h[id]}
			}
			mu.Unlock()
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range ids {
		for city, h := range hc[id].data {
			if cur[id] == nil {
				cur[id] = map[string]cell{}
			}
			c := cur[id][city]
			c.A, c.V, c.L, c.LT = h.A, h.V, h.L, h.LT
			cur[id][city] = c
		}
	}
	return cur, nil
}

// serveIcon returns an item icon from the local cache, downloading it from the official render service once.
func serveIcon(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/icon/")
	if !iconID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	file := filepath.Join(dataDir, "icons", strings.ReplaceAll(id, "@", "_at_")+".png")
	if b, err := os.ReadFile(file); err == nil {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=604800")
		w.Write(b)
		return
	}
	resp, err := httpClient.Get("https://render.albiononline.com/v1/item/" + url.PathEscape(id) + ".png?size=64")
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != 200 {
		http.Error(w, "icon unavailable", 502)
		return
	}
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, b, 0o644)
	w.Header().Set("Content-Type", "image/png")
	w.Write(b)
}

func stateFile() string { return filepath.Join(dataDir, "state.json") }

func newMux(onFocus, onQuit func()) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastPing, pinged = time.Now(), true
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/bye", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		byeAt = time.Now()
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/focus", func(w http.ResponseWriter, r *http.Request) {
		if onFocus != nil {
			onFocus()
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(204)
		go func() { time.Sleep(200 * time.Millisecond); onQuit() }()
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			b, err := os.ReadFile(stateFile())
			if err != nil || !json.Valid(b) {
				w.Write([]byte("{}"))
				return
			}
			w.Write(b)
		case http.MethodPut:
			b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
			if err != nil || !json.Valid(b) {
				http.Error(w, "bad state", 400)
				return
			}
			os.MkdirAll(dataDir, 0o755)
			tmp := stateFile() + ".tmp"
			if err := os.WriteFile(tmp, b, 0o644); err == nil {
				os.Rename(tmp, stateFile())
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	})
	registerUpdateRoutes(mux, onQuit)
	mux.HandleFunc("/api/icon/", serveIcon)
	mux.HandleFunc("/api/prices/extra", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var ids []string
		for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
			if iconID.MatchString(id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 || len(ids) > 120 {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "нужно от 1 до 120 предметов"})
			return
		}
		res, err := loadExtra(r.URL.Query().Get("server"), ids)
		if err != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"prices": res})
	})
	mux.HandleFunc("/api/prices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		res, err := loadPrices(r.URL.Query().Get("server"))
		if err != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(res)
	})
	return mux
}

// watchBrowserTab is used only when the app runs in a normal browser tab (no WebView2):
// it exits when the tab says goodbye and no new page pings within 8 seconds.
func watchBrowserTab() {
	start := time.Now()
	for range time.Tick(time.Second) {
		mu.Lock()
		idle := time.Since(lastPing)
		p := pinged
		bye := !byeAt.IsZero() && lastPing.Before(byeAt) && time.Since(byeAt) > 8*time.Second
		mu.Unlock()
		if bye || (p && idle > 10*time.Minute) || (!p && time.Since(start) > 3*time.Minute) {
			os.Exit(0)
		}
	}
}

func appDataDir() string {
	base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, appName)
}
