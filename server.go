package main

import (
	"crypto/ed25519"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
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

// CachedState holds the latest weather and chart data in memory
type CachedState struct {
	mu           sync.RWMutex
	data         WeatherResponse
	chartPNG     []byte
	lastUpdated  time.Time
	currentIdx   int
	currentP     float64
	diff3h       float64
	currentT     float64
	statusAlert  string
	statusCode   string
	colorHex     string
}

type APIPressureResponse struct {
	UpdatedAt     string            `json:"updated_at"`
	CurrentP      float64           `json:"current_pressure"`
	Diff3h        float64           `json:"diff_3h"`
	Temperature   float64           `json:"temperature"`
	Status        string            `json:"status"`
	StatusMessage string            `json:"status_message"`
	Series        []TimeSeriesPoint `json:"series"`
}

type TimeSeriesPoint struct {
	Time      string  `json:"time"`
	Pressure  float64 `json:"pressure"`
	IsCurrent bool    `json:"is_current"`
}

var globalCache = &CachedState{}

func updateCache(lat, lon float64) error {
	weatherURL := fmt.Sprintf(apiURL, lat, lon)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(weatherURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("open-meteo returned status %d", resp.StatusCode)
	}

	var data WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	chartBytes, err := generateChartPNG(data, 24, 24)
	if err != nil {
		return err
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

	globalCache.mu.Lock()
	globalCache.data = data
	globalCache.chartPNG = chartBytes
	globalCache.lastUpdated = time.Now()
	globalCache.currentIdx = currentIdx
	globalCache.currentP = currentP
	globalCache.diff3h = diff3h
	globalCache.currentT = currentT
	globalCache.statusAlert = statusAlert
	globalCache.statusCode = statusCode
	globalCache.colorHex = colorHex
	globalCache.mu.Unlock()

	fmt.Printf("[%s] Cache successfully refreshed. Pressure: %.1f hPa (Diff: %+.1f hPa)\n",
		time.Now().Format("2006-01-02 15:04:05"), currentP, diff3h)
	return nil
}

// Background poller every 1 hour
func startBackgroundPoller(lat, lon float64, webhookURL string) {
	// Initial fetch
	if err := updateCache(lat, lon); err != nil {
		fmt.Printf("Initial cache fetch failed: %v\n", err)
	}

	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for range ticker.C {
			if err := updateCache(lat, lon); err != nil {
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

func startServer(port int, pubKeyHex, webhookURL string, lat, lon float64) error {
	startBackgroundPoller(lat, lon, webhookURL)

	// Liveness/Readiness probe
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// JSON API: GET /api/v1/pressure
	http.HandleFunc("/api/v1/pressure", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		globalCache.mu.RLock()
		defer globalCache.mu.RUnlock()

		if len(globalCache.data.Hourly.Time) == 0 {
			http.Error(w, "Data not ready yet", http.StatusServiceUnavailable)
			return
		}

		// Build time series (48h: 24h past + 24h future)
		startIdx := max(0, globalCache.currentIdx-24)
		endIdx := min(len(globalCache.data.Hourly.Time), globalCache.currentIdx+25)

		var series []TimeSeriesPoint
		for i := startIdx; i < endIdx; i++ {
			series = append(series, TimeSeriesPoint{
				Time:      globalCache.data.Hourly.Time[i],
				Pressure:  globalCache.data.Hourly.SurfacePressure[i],
				IsCurrent: (i == globalCache.currentIdx),
			})
		}

		resp := APIPressureResponse{
			UpdatedAt:     globalCache.lastUpdated.Format(time.RFC3339),
			CurrentP:      globalCache.currentP,
			Diff3h:        globalCache.diff3h,
			Temperature:   globalCache.currentT,
			Status:        globalCache.statusCode,
			StatusMessage: globalCache.statusAlert,
			Series:        series,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Image API: GET /api/v1/chart.png
	http.HandleFunc("/api/v1/chart.png", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		globalCache.mu.RLock()
		pngBytes := globalCache.chartPNG
		globalCache.mu.RUnlock()

		if len(pngBytes) == 0 {
			http.Error(w, "Chart not generated yet", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=600")
		w.Write(pngBytes)
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
			globalCache.mu.RLock()
			currentP := globalCache.currentP
			diff3h := globalCache.diff3h
			currentT := globalCache.currentT
			statusAlert := globalCache.statusAlert
			colorInt := 0x89dceb
			if globalCache.statusCode == "caution" {
				colorInt = 0xf38ba8
			} else if globalCache.statusCode == "notice" {
				colorInt = 0xf9e2af
			}
			chartBytes := globalCache.chartPNG
			cachedData := globalCache.data
			globalCache.mu.RUnlock()

			desc := fmt.Sprintf("• **現在気圧**: `%.1f hPa`\n• **3h 変動**: `%+.1f hPa`\n• **気温**: `%.1f °C`\n• **判定**: %s",
				currentP, diff3h, currentT, statusAlert)

			if webhookURL != "" && len(chartBytes) > 0 {
				go func() {
					sendDiscordWebhook(webhookURL, chartBytes, cachedData)
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
