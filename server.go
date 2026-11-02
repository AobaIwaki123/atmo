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

const (
	locationRefreshInterval = time.Hour
	// The public Open-Meteo endpoint is called only by the refresh loop. Keeping a
	// small pause between locations also avoids bursty traffic during a refresh.
	upstreamRequestSpacing = 1200 * time.Millisecond
	locationMatchRadiusKm  = 20.0
)

// LocationDefinition is one of the 47 supported representative points. The
// API accepts coordinates only to select one of these definitions; it never
// creates a new upstream request for arbitrary user-supplied coordinates.
type LocationDefinition struct {
	Prefecture     string
	Representative string
	Latitude       float64
	Longitude      float64
}

var locationCatalog = []LocationDefinition{
	{Prefecture: "北海道", Representative: "札幌", Latitude: 43.0642, Longitude: 141.3469},
	{Prefecture: "青森県", Representative: "青森", Latitude: 40.8244, Longitude: 140.7400},
	{Prefecture: "岩手県", Representative: "盛岡", Latitude: 39.7036, Longitude: 141.1525},
	{Prefecture: "宮城県", Representative: "仙台", Latitude: 38.2689, Longitude: 140.8719},
	{Prefecture: "秋田県", Representative: "秋田", Latitude: 39.7186, Longitude: 140.1025},
	{Prefecture: "山形県", Representative: "山形", Latitude: 38.2406, Longitude: 140.3633},
	{Prefecture: "福島県", Representative: "福島", Latitude: 37.7500, Longitude: 140.4678},
	{Prefecture: "茨城県", Representative: "水戸", Latitude: 36.3414, Longitude: 140.4467},
	{Prefecture: "栃木県", Representative: "宇都宮", Latitude: 36.5658, Longitude: 139.8836},
	{Prefecture: "群馬県", Representative: "前橋", Latitude: 36.3911, Longitude: 139.0608},
	{Prefecture: "埼玉県", Representative: "さいたま", Latitude: 35.8569, Longitude: 139.6489},
	{Prefecture: "千葉県", Representative: "千葉", Latitude: 35.6047, Longitude: 140.1233},
	{Prefecture: "東京都", Representative: "東京", Latitude: 35.6895, Longitude: 139.6917},
	{Prefecture: "神奈川県", Representative: "横浜", Latitude: 35.4478, Longitude: 139.6425},
	{Prefecture: "新潟県", Representative: "新潟", Latitude: 37.9022, Longitude: 139.0236},
	{Prefecture: "富山県", Representative: "富山", Latitude: 36.6953, Longitude: 137.2114},
	{Prefecture: "石川県", Representative: "金沢", Latitude: 36.5944, Longitude: 136.6256},
	{Prefecture: "福井県", Representative: "福井", Latitude: 36.0653, Longitude: 136.2219},
	{Prefecture: "山梨県", Representative: "甲府", Latitude: 35.6639, Longitude: 138.5683},
	{Prefecture: "長野県", Representative: "長野", Latitude: 36.6514, Longitude: 138.1811},
	{Prefecture: "岐阜県", Representative: "岐阜", Latitude: 35.3911, Longitude: 136.7222},
	{Prefecture: "静岡県", Representative: "静岡", Latitude: 34.9769, Longitude: 138.3831},
	{Prefecture: "愛知県", Representative: "名古屋", Latitude: 35.1803, Longitude: 136.9067},
	{Prefecture: "三重県", Representative: "津", Latitude: 34.7303, Longitude: 136.5086},
	{Prefecture: "滋賀県", Representative: "大津", Latitude: 35.0044, Longitude: 135.8683},
	{Prefecture: "京都府", Representative: "京都", Latitude: 35.0214, Longitude: 135.7556},
	{Prefecture: "大阪府", Representative: "大阪", Latitude: 34.6864, Longitude: 135.5200},
	{Prefecture: "兵庫県", Representative: "神戸", Latitude: 34.6914, Longitude: 135.1831},
	{Prefecture: "奈良県", Representative: "奈良", Latitude: 34.6853, Longitude: 135.8328},
	{Prefecture: "和歌山県", Representative: "和歌山", Latitude: 34.2261, Longitude: 135.1675},
	{Prefecture: "鳥取県", Representative: "鳥取", Latitude: 35.5036, Longitude: 134.2383},
	{Prefecture: "島根県", Representative: "松江", Latitude: 35.4722, Longitude: 133.0506},
	{Prefecture: "岡山県", Representative: "岡山", Latitude: 34.6617, Longitude: 133.9350},
	{Prefecture: "広島県", Representative: "広島", Latitude: 34.3964, Longitude: 132.4594},
	{Prefecture: "山口県", Representative: "山口", Latitude: 34.1858, Longitude: 131.4714},
	{Prefecture: "徳島県", Representative: "徳島", Latitude: 34.0658, Longitude: 134.5594},
	{Prefecture: "香川県", Representative: "高松", Latitude: 34.3403, Longitude: 134.0433},
	{Prefecture: "愛媛県", Representative: "松山", Latitude: 33.8417, Longitude: 132.7658},
	{Prefecture: "高知県", Representative: "高知", Latitude: 33.5597, Longitude: 133.5311},
	{Prefecture: "福岡県", Representative: "福岡", Latitude: 33.6064, Longitude: 130.4181},
	{Prefecture: "佐賀県", Representative: "佐賀", Latitude: 33.2494, Longitude: 130.2989},
	{Prefecture: "長崎県", Representative: "長崎", Latitude: 32.7447, Longitude: 129.8736},
	{Prefecture: "熊本県", Representative: "熊本", Latitude: 32.7897, Longitude: 130.7417},
	{Prefecture: "大分県", Representative: "大分", Latitude: 33.2381, Longitude: 131.6125},
	{Prefecture: "宮崎県", Representative: "宮崎", Latitude: 31.9111, Longitude: 131.4239},
	{Prefecture: "鹿児島県", Representative: "鹿児島", Latitude: 31.5603, Longitude: 130.5581},
	{Prefecture: "沖縄県", Representative: "那覇", Latitude: 26.2125, Longitude: 127.6811},
}

// LocationEntry holds the weather and chart data for one supported point.
type LocationEntry struct {
	Data         WeatherResponse
	ChartPNG     []byte
	LastUpdated  time.Time
	CurrentIdx   int
	CurrentP     float64
	Diff3h       float64
	CurrentT     float64
	StatusAlert  string
	StatusCode   string
	ColorHex     string
	Latitude     float64
	Longitude    float64
	LocationName string
}

// LocationCache manages the fixed supported locations in memory.
type LocationCache struct {
	mu          sync.RWMutex
	refreshMu   sync.Mutex
	items       map[string]*LocationEntry
	initialized bool
}

var locationCache = &LocationCache{
	items: make(map[string]*LocationEntry),
}

func distanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func resolveLocation(lat, lon float64) (LocationDefinition, bool) {
	var best LocationDefinition
	bestDistance := math.MaxFloat64
	for _, location := range locationCatalog {
		distance := distanceKm(lat, lon, location.Latitude, location.Longitude)
		if distance < bestDistance {
			best = location
			bestDistance = distance
		}
	}
	return best, bestDistance <= locationMatchRadiusKm
}

func requestedLocation(r *http.Request, defaultLat, defaultLon float64) (LocationDefinition, error) {
	lat := defaultLat
	lon := defaultLon
	if value := r.URL.Query().Get("lat"); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < -90 || parsed > 90 {
			return LocationDefinition{}, fmt.Errorf("invalid latitude")
		}
		lat = parsed
	}
	if value := r.URL.Query().Get("lon"); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < -180 || parsed > 180 {
			return LocationDefinition{}, fmt.Errorf("invalid longitude")
		}
		lon = parsed
	}

	location, ok := resolveLocation(lat, lon)
	if !ok {
		return LocationDefinition{}, fmt.Errorf("unsupported location")
	}
	return location, nil
}

func fetchLocation(location LocationDefinition) (*LocationEntry, error) {
	weatherURL := fmt.Sprintf(apiURL, location.Latitude, location.Longitude)
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
	if len(data.Hourly.Time) == 0 || len(data.Hourly.SurfacePressure) == 0 || len(data.Hourly.Temperature2m) == 0 {
		return nil, fmt.Errorf("open-meteo returned empty hourly data")
	}

	currentIdx := getCurrentIndex(data.Hourly.Time)
	if currentIdx >= len(data.Hourly.SurfacePressure) || currentIdx >= len(data.Hourly.Temperature2m) {
		return nil, fmt.Errorf("open-meteo returned inconsistent hourly data")
	}

	chartBytes, err := generateChartPNG(data, 24, 24)
	if err != nil {
		chartBytes = nil
	}

	now := time.Now()
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

	return &LocationEntry{
		Data:         data,
		ChartPNG:     chartBytes,
		LastUpdated:  now,
		CurrentIdx:   currentIdx,
		CurrentP:     currentP,
		Diff3h:       diff3h,
		CurrentT:     currentT,
		StatusAlert:  statusAlert,
		StatusCode:   statusCode,
		ColorHex:     colorHex,
		Latitude:     location.Latitude,
		Longitude:    location.Longitude,
		LocationName: location.Prefecture,
	}, nil
}

func refreshLocation(location LocationDefinition) error {
	entry, err := fetchLocation(location)
	if err != nil {
		return err
	}

	locationCache.mu.Lock()
	locationCache.items[location.Prefecture] = entry
	locationCache.mu.Unlock()

	fmt.Printf("[%s] %s cached. Pressure: %.1f hPa (Diff: %+.1f hPa)\n",
		entry.LastUpdated.Format("2006-01-02 15:04:05"), location.Prefecture, entry.CurrentP, entry.Diff3h)
	return nil
}

func refreshAllLocations() {
	locationCache.refreshMu.Lock()
	defer locationCache.refreshMu.Unlock()

	startedAt := time.Now()
	successCount := 0
	for index, location := range locationCatalog {
		if index > 0 {
			time.Sleep(upstreamRequestSpacing)
		}
		if err := refreshLocation(location); err != nil {
			fmt.Printf("[%s] %s refresh failed: %v\n", time.Now().Format("2006-01-02 15:04:05"), location.Prefecture, err)
			continue
		}
		successCount++
	}

	locationCache.mu.Lock()
	locationCache.initialized = true
	locationCache.mu.Unlock()
	fmt.Printf("[%s] Location refresh complete: %d/%d succeeded in %s\n",
		time.Now().Format("2006-01-02 15:04:05"), successCount, len(locationCatalog), time.Since(startedAt).Round(time.Second))
}

func getCachedLocation(location LocationDefinition) (*LocationEntry, error) {
	locationCache.mu.RLock()
	entry, exists := locationCache.items[location.Prefecture]
	locationCache.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("location data is not ready")
	}
	return entry, nil
}

func isLocationCacheReady() bool {
	locationCache.mu.RLock()
	defer locationCache.mu.RUnlock()
	return locationCache.initialized
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
	LocationName  string            `json:"location_name"`
	Series        []TimeSeriesPoint `json:"series"`
}

type TimeSeriesPoint struct {
	Time      string  `json:"time"`
	Pressure  float64 `json:"pressure"`
	IsCurrent bool    `json:"is_current"`
}

// Background poller refreshes the fixed 47-location catalog once per hour.
// Request handlers only read this cache and never call upstream services.
func startBackgroundPoller() {
	go func() {
		refreshAllLocations()

		ticker := time.NewTicker(locationRefreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			refreshAllLocations()
		}
	}()
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
}

func startServer(port int, pubKeyHex, webhookURL string, defaultServerLat, defaultServerLon float64) error {
	startBackgroundPoller()

	// Liveness/Readiness probe
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if !isLocationCacheReady() {
			http.Error(w, "location cache is warming up", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("READY"))
	})

	// JSON API: GET /api/v1/pressure?lat=35.68&lon=139.77
	http.HandleFunc("/api/v1/pressure", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		location, err := requestedLocation(r, defaultServerLat, defaultServerLon)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		entry, err := getCachedLocation(location)
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
			LocationName:  entry.LocationName,
			Series:        series,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Image API: GET /api/v1/chart.png?lat=...&lon=...
	http.HandleFunc("/api/v1/chart.png", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)
		location, err := requestedLocation(r, defaultServerLat, defaultServerLon)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		entry, err := getCachedLocation(location)
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
			location, ok := resolveLocation(defaultServerLat, defaultServerLon)
			if !ok {
				http.Error(w, "Default location is unsupported", http.StatusInternalServerError)
				return
			}
			entry, err := getCachedLocation(location)
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
