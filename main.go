package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultLat = 35.6812 // Tokyo Station (Chiyoda, Tokyo)
	defaultLon = 139.7671
	apiURL     = "https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&hourly=surface_pressure,temperature_2m&timezone=Asia%%2FTokyo"

	colorReset  = "\033[0m"
	colorRed    = "\033[91m"
	colorGreen  = "\033[92m"
	colorYellow = "\033[93m"
	colorCyan   = "\033[96m"
	colorBold   = "\033[1m"
)

type WeatherResponse struct {
	Hourly struct {
		Time            []string  `json:"time"`
		SurfacePressure []float64 `json:"surface_pressure"`
		Temperature2m   []float64 `json:"temperature_2m"`
	} `json:"hourly"`
}

func getCurrentIndex(times []string) int {
	jst := time.FixedZone("JST", 9*3600)
	now := time.Now().In(jst)
	nowFormatted := now.Format("2006-01-02T15:00")

	currentIdx := 0
	for i, t := range times {
		if t <= nowFormatted {
			currentIdx = i
		}
	}
	return currentIdx
}

func main() {
	lat := flag.Float64("lat", defaultLat, "Latitude")
	lon := flag.Float64("lon", defaultLon, "Longitude")
	hoursBefore := flag.Int("before", 24, "Hours in the past to display")
	hoursAhead := flag.Int("ahead", 24, "Hours in the future to display")
	chartHeight := flag.Int("height", 10, "Height of ASCII chart")
	outPath := flag.String("out", "", "Output PNG chart image file path")
	webhookURL := flag.String("webhook", os.Getenv("DISCORD_WEBHOOK_URL"), "Discord webhook URL to post chart")
	isServe := flag.Bool("serve", false, "Run as Discord HTTP interaction server")
	serverPort := flag.Int("port", 8080, "Port for HTTP interaction server")
	discordPubKey := flag.String("public-key", os.Getenv("DISCORD_PUBLIC_KEY"), "Discord application public key for Ed25519 signature verification")
	flag.Parse()

	if *isServe {
		if err := startServer(*serverPort, *discordPubKey, *webhookURL, *lat, *lon); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	url := fmt.Sprintf(apiURL, *lat, *lon)
	client := &http.Client{Timeout: 30 * time.Second}
	var resp *http.Response
	var err error
	for retry := 0; retry < 3; retry++ {
		resp, err = client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching weather data: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Open-Meteo API returned HTTP status %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var data WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing API response: %v\n", err)
		os.Exit(1)
	}

	// If PNG generation or Discord webhook is requested
	if *outPath != "" || *webhookURL != "" {
		pngBytes, err := generateChartPNG(data, *hoursBefore, *hoursAhead)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating PNG: %v\n", err)
			os.Exit(1)
		}

		if *outPath != "" {
			if err := os.WriteFile(*outPath, pngBytes, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving PNG to %s: %v\n", *outPath, err)
				os.Exit(1)
			}
			fmt.Printf("Saved chart image to %s\n", *outPath)
		}

		if *webhookURL != "" {
			if err := sendDiscordWebhook(*webhookURL, pngBytes, data); err != nil {
				fmt.Fprintf(os.Stderr, "Error sending to Discord webhook: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	// Default: ASCII chart
	renderChart(data, *hoursBefore, *hoursAhead, *chartHeight)
}

func renderChart(data WeatherResponse, before, ahead, height int) {
	times := data.Hourly.Time
	pressures := data.Hourly.SurfacePressure
	temps := data.Hourly.Temperature2m

	if len(times) == 0 || len(pressures) == 0 {
		fmt.Println("No data available.")
		return
	}

	currentIdx := getCurrentIndex(times)
	currentP := pressures[currentIdx]
	prev3hIdx := currentIdx - 3
	if prev3hIdx < 0 {
		prev3hIdx = 0
	}
	diff3h := currentP - pressures[prev3hIdx]

	alert := fmt.Sprintf("%sSTABLE (安全・安定)%s", colorGreen, colorReset)
	if math.Abs(diff3h) >= 3.0 {
		alert = fmt.Sprintf("%s%s⚠️ CAUTION: 急変警告 (3hで %+.1f hPa)%s", colorRed, colorBold, diff3h, colorReset)
	} else if math.Abs(diff3h) >= 1.5 {
		alert = fmt.Sprintf("%sNOTICE: 注意 (3hで %+.1f hPa)%s", colorYellow, diff3h, colorReset)
	}

	startIdx := currentIdx - before
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := currentIdx + ahead + 1
	if endIdx > len(times) {
		endIdx = len(times)
	}

	subTimes := times[startIdx:endIdx]
	subP := pressures[startIdx:endIdx]
	width := len(subP)

	minP := subP[0]
	maxP := subP[0]
	for _, p := range subP {
		if p < minP {
			minP = p
		}
		if p > maxP {
			maxP = p
		}
	}
	pRange := maxP - minP
	if pRange < 1.0 {
		pRange = 1.0
	}

	nowRelativeIdx := currentIdx - startIdx

	grid := make([][]string, height)
	for r := 0; r < height; r++ {
		grid[r] = make([]string, width)
		for c := 0; c < width; c++ {
			grid[r][c] = " "
		}
	}

	for x, p := range subP {
		y := int(((p - minP) / pRange) * float64(height-1))
		row := height - 1 - y
		if row < 0 {
			row = 0
		}
		if row >= height {
			row = height - 1
		}

		if x == nowRelativeIdx {
			grid[row][x] = fmt.Sprintf("%s%s▲%s", colorBold, colorYellow, colorReset)
			for r := 0; r < height; r++ {
				if r != row {
					grid[r][x] = fmt.Sprintf("%s│%s", colorYellow, colorReset)
				}
			}
		} else {
			grid[row][x] = fmt.Sprintf("%s•%s", colorCyan, colorReset)
		}
	}

	sep := strings.Repeat("=", 68)
	fmt.Println(sep)
	fmt.Printf("%s%s ATMO: BAROMETRIC PRESSURE CHART (JST) %s\n", colorBold, colorCyan, colorReset)
	fmt.Println(sep)
	fmt.Printf("現在時刻: %s | 気圧: %s%.1f hPa%s | 気温: %.1f°C\n",
		times[currentIdx], colorBold, currentP, colorReset, temps[currentIdx])
	fmt.Printf("変動ステータス: %s\n", alert)
	fmt.Printf("凡例: %s▲ 現在時刻%s | %s• 気圧プロット%s | 単位: hPa\n", colorYellow, colorReset, colorCyan, colorReset)
	fmt.Println(strings.Repeat("-", 68))

	for r := 0; r < height; r++ {
		val := maxP - (float64(r) / float64(height-1) * pRange)
		line := fmt.Sprintf("%6.1f | ", val)
		for c := 0; c < width; c++ {
			line += grid[r][c]
		}
		fmt.Println(line)
	}

	fmt.Printf("       +-%s\n", strings.Repeat("-", width))

	tickLine := "         "
	timeLine := "         "
	for x := 0; x < width; x++ {
		t := subTimes[x]
		h := t[len(t)-5 : len(t)-3]
		if x%6 == 0 || x == nowRelativeIdx {
			tickLine += "^"
			timeLine += h
			x++
			if x < width {
				tickLine += " "
				timeLine += " "
			}
		} else {
			tickLine += " "
			timeLine += " "
		}
	}

	fmt.Println(tickLine)
	fmt.Printf("HOUR   | %s\n", timeLine[9:])
	fmt.Println(sep)
}
