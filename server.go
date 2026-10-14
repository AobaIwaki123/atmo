package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

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

func startServer(port int, pubKeyHex, webhookURL string, lat, lon float64) error {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

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

		// Verify signature if DISCORD_PUBLIC_KEY is provided
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

		// 1. PING verification from Discord Developer Portal
		if req.Type == interactionTypePing {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(InteractionResponse{Type: interactionCallbackTypePong})
			return
		}

		// 2. Application Command (/atmo)
		if req.Type == interactionTypeApplicationCommand {
			// Fetch latest weather data
			weatherURL := fmt.Sprintf(apiURL, lat, lon)
			client := &http.Client{Timeout: 10}
			resp, err := client.Get(weatherURL)
			if err != nil {
				writeErrorResponse(w, "気象データの取得に失敗しました。")
				return
			}
			defer resp.Body.Close()

			var data WeatherResponse
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				writeErrorResponse(w, "気象データのパースに失敗しました。")
				return
			}

			// Generate chart image in background or webhook if needed
			// Immediate ephemeral or message response
			currentIdx := getCurrentIndex(data.Hourly.Time)
			currentP := data.Hourly.SurfacePressure[currentIdx]
			prev3hIdx := max(0, currentIdx-3)
			diff3h := currentP - data.Hourly.SurfacePressure[prev3hIdx]
			currentT := data.Hourly.Temperature2m[currentIdx]

			statusAlert := "🟢 安全・安定"
			colorInt := 0x89dceb
			if diff3h >= 3.0 || diff3h <= -3.0 {
				colorInt = 0xf38ba8
				statusAlert = fmt.Sprintf("⚠️ **急変警告** (3時間で %+.1f hPa)", diff3h)
			} else if diff3h >= 1.5 || diff3h <= -1.5 {
				colorInt = 0xf9e2af
				statusAlert = fmt.Sprintf("🟡 **変動注意** (3時間で %+.1f hPa)", diff3h)
			}

			desc := fmt.Sprintf("• **現在気圧**: `%.1f hPa`\n• **3h 変動**: `%+.1f hPa`\n• **気温**: `%.1f °C`\n• **判定**: %s",
				currentP, diff3h, currentT, statusAlert)

			// If webhook is provided, also send image to channel
			if webhookURL != "" {
				go func() {
					pngBytes, err := generateChartPNG(data, 24, 24)
					if err == nil {
						sendDiscordWebhook(webhookURL, pngBytes, data)
					}
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

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting atmo HTTP interaction server on %s (Endpoints: /interactions, /healthz)...\n", addr)
	return http.ListenAndServe(addr, nil)
}

func writeErrorResponse(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(InteractionResponse{
		Type: interactionCallbackTypeChannelMessageWithSource,
		Data: &InteractionResponseData{
			Content: fmt.Sprintf("⚠️ %s", msg),
		},
	})
}
