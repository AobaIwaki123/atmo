package main

import (
	"crypto/ed25519"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

//go:embed web/*
var embeddedWebFS embed.FS

// Discord Interaction types
const (
	interactionTypePing               = 1
	interactionTypeApplicationCommand = 2

	interactionCallbackTypePong                     = 1
	interactionCallbackTypeChannelMessageWithSource = 4
)

type InteractionRequest struct {
	Type int `json:"type"`
	Data struct {
		Name string `json:"name"`
	} `json:"data"`
}

type InteractionResponse struct {
	Type int                      `json:"type"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

type InteractionResponseData struct {
	Content string         `json:"content,omitempty"`
	Embeds  []DiscordEmbed `json:"embeds,omitempty"`
}

// LocationEntry holds the weather and chart data for a specific rounded grid coordinate
type LocationEntry struct {
	Data        WeatherResponse
	ChartPNG    []byte
	LastUpdated time.Time
	ExpireAt    time.Time
	CurrentIdx  int
	CurrentP    float64
	Diff3h      float64
	CurrentT    float64
	StatusAlert string
	StatusCode  string
	ColorHex    string
	Latitude    float64
	Longitude   float64
}

// LocationCache manages multiple grid-rounded locations in memory
type LocationCache struct {
	mu    sync.RWMutex
	items map[string]*LocationEntry
}

var locationCache = &LocationCache{
	items: make(map[string]*LocationEntry),
}

func getGridKey(lat, lon float64) (string, float64, float64) {
	roundedLat := math.Round(lat*100) / 100
	roundedLon := math.Round(lon*100) / 100
	return fmt.Sprintf("%.2f,%.2f", roundedLat, roundedLon), roundedLat, roundedLon
}

func getOrFetchLocation(lat, lon float64) (*LocationEntry, error) {
	key, rLat, rLon := getGridKey(lat, lon)
	now := time.Now()

	// Read lock check
	locationCache.mu.RLock()
	entry, exists := locationCache.items[key]
	if exists && now.Before(entry.ExpireAt) {
		locationCache.mu.RUnlock()
		return entry, nil
	}
	locationCache.mu.RUnlock()

	// Write lock for fetch & update
	locationCache.mu.Lock()
	defer locationCache.mu.Unlock()

	if entry, exists := locationCache.items[key]; exists && now.Before(entry.ExpireAt) {
		return entry, nil
	}

	weatherURL := fmt.Sprintf(apiURL, rLat, rLon)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(weatherURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open-meteo returned status %d", resp.StatusCode)
	}

	var data WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	chartBytes, err := generateChartPNG(data, 24, 24)
	if err != nil {
		chartBytes = nil
	}

	currentIdx := getCurrentIndex(data.Hourly.Time)
	currentP := data.Hourly.SurfacePressure[currentIdx]
	prev3hIdx := max(0, currentIdx-3)
	diff3h := currentP - data.Hourly.SurfacePressure[prev3hIdx]
	currentT := data.Hourly.Temperature2m[currentIdx]

	statusAlert := "🟢 安全・安定"
	statusCode := "stable"
	colorHex := "#89dceb"

	if diff3h >= 3.0 || diff3h <= -3.0 {
		statusAlert = fmt.Sprintf("⚠️ 急変警告 (3時間で %+.1f hPa)", diff3h)
		statusCode = "caution"
		colorHex = "#f38ba8"
	} else if diff3h >= 1.5 || diff3h <= -1.5 {
		statusAlert = fmt.Sprintf("🟡 変動注意 (3時間で %+.1f hPa)", diff3h)
		statusCode = "notice"
		colorHex = "#f9e2af"
	}

	newEntry := &LocationEntry{
		Data:        data,
		ChartPNG:    chartBytes,
		LastUpdated: now,
		ExpireAt:    now.Add(10 * time.Minute),
		CurrentIdx:  currentIdx,
		CurrentP:    currentP,
		Diff3h:      diff3h,
		CurrentT:    currentT,
		StatusAlert: statusAlert,
		StatusCode:  statusCode,
		ColorHex:    colorHex,
		Latitude:    rLat,
		Longitude:   rLon,
	}

	// Evict expired entries if cache grows large
	if len(locationCache.items) > 500 {
		for k, v := range locationCache.items {
			if now.After(v.ExpireAt) {
				delete(locationCache.items, k)
			}
		}
	}

	locationCache.items[key] = newEntry
	fmt.Printf("[%s] Grid (%s) cached. Pressure: %.1f hPa (Diff: %+.1f hPa)\n",
		now.Format("2006-01-02 15:04:05"), key, currentP, diff3h)

	return newEntry, nil
}

type APIPressureResponse struct {
	UpdatedAt     string            `json:"updated_at"`
	CurrentP      float64           `json:"current_pressure"`
	Diff3h        float64           `json:"diff_3h"`
	Temperature   float64           `json:"temperature"`
	Status        string            `json:"status"`
	StatusMessage string            `json:"status_message"`
	Latitude      float64           `json:"latitude"`
	Longitude     float64           `json:"longitude"`
	Series        []TimeSeriesPoint `json:"series"`
}

type TimeSeriesPoint struct {
	Time      string  `json:"time"`
	Pressure  float64 `json:"pressure"`
	IsCurrent bool    `json:"is_current"`
}

// Background poller every 1 hour for default location (Discord webhook)
func startBackgroundPoller(lat, lon float64, webhookURL string) {
	// Initial fetch
	if _, err := getOrFetchLocation(lat, lon); err != nil {
		fmt.Printf("Initial cache fetch failed: %v\n", err)
	}

	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for range ticker.C {
			if _, err := getOrFetchLocation(lat, lon); err != nil {
				fmt.Printf("Periodic cache update failed: %v\n", err)
			}
		}
	}()
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
}

func startServer(port int, pubKeyHex, webhookURL string, defaultServerLat, defaultServerLon float64) error {
	startBackgroundPoller(defaultServerLat, defaultServerLon, webhookURL)

	// Liveness/Readiness probe
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// JSON API: GET /api/v1/pressure?lat=35.68&lon=139.77
	http.HandleFunc("/api/v1/pressure", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		reqLat := defaultServerLat
		reqLon := defaultServerLon
		if latStr := r.URL.Query().Get("lat"); latStr != "" {
			if v, err := strconv.ParseFloat(latStr, 64); err == nil && v >= -90 && v <= 90 {
				reqLat = v
			}
		}
		if lonStr := r.URL.Query().Get("lon"); lonStr != "" {
			if v, err := strconv.ParseFloat(lonStr, 64); err == nil && v >= -180 && v <= 180 {
				reqLon = v
			}
		}

		entry, err := getOrFetchLocation(reqLat, reqLon)
		if err != nil {
			http.Error(w, fmt.Sprintf("Data fetch error: %v", err), http.StatusServiceUnavailable)
			return
		}

		// Build time series (48h: 24h past + 24h future)
		startIdx := max(0, entry.CurrentIdx-24)
		endIdx := min(len(entry.Data.Hourly.Time), entry.CurrentIdx+25)

		var series []TimeSeriesPoint
		for i := startIdx; i < endIdx; i++ {
			series = append(series, TimeSeriesPoint{
				Time:      entry.Data.Hourly.Time[i],
				Pressure:  entry.Data.Hourly.SurfacePressure[i],
				IsCurrent: (i == entry.CurrentIdx),
			})
		}

		resp := APIPressureResponse{
			UpdatedAt:     entry.LastUpdated.Format(time.RFC3339),
			CurrentP:      entry.CurrentP,
			Diff3h:        entry.Diff3h,
			Temperature:   entry.CurrentT,
			Status:        entry.StatusCode,
			StatusMessage: entry.StatusAlert,
			Latitude:      entry.Latitude,
			Longitude:     entry.Longitude,
			Series:        series,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Image API: GET /api/v1/chart.png?lat=...&lon=...
	http.HandleFunc("/api/v1/chart.png", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		reqLat := defaultServerLat
		reqLon := defaultServerLon
		if latStr := r.URL.Query().Get("lat"); latStr != "" {
			if v, err := strconv.ParseFloat(latStr, 64); err == nil && v >= -90 && v <= 90 {
				reqLat = v
			}
		}
		if lonStr := r.URL.Query().Get("lon"); lonStr != "" {
			if v, err := strconv.ParseFloat(lonStr, 64); err == nil && v >= -180 && v <= 180 {
				reqLon = v
			}
		}

		entry, err := getOrFetchLocation(reqLat, reqLon)
		if err != nil || len(entry.ChartPNG) == 0 {
			http.Error(w, "Chart not generated yet", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=600")
		w.Write(entry.ChartPNG)
	})

	// Discord Interactions endpoint
	http.HandleFunc("/interactions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Cannot read body", http.StatusBadRequest)
			return
		}

		if pubKeyHex != "" {
			if !verifyDiscordSignature(pubKeyHex, r, body) {
				http.Error(w, "Invalid signature", http.StatusUnauthorized)
				return
			}
		}

		var req InteractionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "Invalid json", http.StatusBadRequest)
			return
		}

		if req.Type == interactionTypePing {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(InteractionResponse{Type: interactionCallbackTypePong})
			return
		}

		if req.Type == interactionTypeApplicationCommand {
			entry, err := getOrFetchLocation(defaultServerLat, defaultServerLon)
			if err != nil {
				http.Error(w, "Weather data unavailable", http.StatusServiceUnavailable)
				return
			}

			colorInt := 0x89dceb
			if entry.StatusCode == "caution" {
				colorInt = 0xf38ba8
			} else if entry.StatusCode == "notice" {
				colorInt = 0xf9e2af
			}

			desc := fmt.Sprintf("• **現在気圧**: `%.1f hPa`\n• **3h 変動**: `%+.1f hPa`\n• **気温**: `%.1f °C`\n• **判定**: %s",
				entry.CurrentP, entry.Diff3h, entry.CurrentT, entry.StatusAlert)

			if webhookURL != "" && len(entry.ChartPNG) > 0 {
				go func() {
					sendDiscordWebhook(webhookURL, entry.ChartPNG, entry.Data)
				}()
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(InteractionResponse{
				Type: interactionCallbackTypeChannelMessageWithSource,
				Data: &InteractionResponseData{
					Embeds: []DiscordEmbed{
						{
							Title:       "📊 気圧モニター",
							Description: desc + "\n\n*(詳細なグラフ画像をチャンネルに送信中...)*",
							Color:       colorInt,
						},
					},
				},
			})
			return
		}

		http.Error(w, "Unknown interaction", http.StatusBadRequest)
	})

	// Web Frontend (Flutter Web) embedded at root /
	webSubFS, err := fs.Sub(embeddedWebFS, "web")
	if err == nil {
		fileServer := http.FileServer(http.FS(webSubFS))
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			enableCORS(w)
			p := r.URL.Path
			if p == "/" || p == "/index.html" || p == "/flutter_bootstrap.js" ||
				p == "/flutter_service_worker.js" || p == "/version.json" ||
				p == "/main.dart.js" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting atmo Caching API Server on %s (Endpoints: /api/v1/pressure, /api/v1/chart.png, /healthz, /interactions)...\n", addr)
	return http.ListenAndServe(addr, nil)
}

func verifyDiscordSignature(pubKeyHex string, r *http.Request, body []byte) bool {
	sigHex := r.Header.Get("X-Signature-Ed25519")
	timestamp := r.Header.Get("X-Signature-Timestamp")
	if sigHex == "" || timestamp == "" {
		return false
	}

	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return false
	}

	msg := append([]byte(timestamp), body...)
	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), msg, sig)
}
