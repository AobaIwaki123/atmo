package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type DiscordWebhookPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []DiscordEmbed `json:"embeds,omitempty"`
}

type DiscordEmbed struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Color       int           `json:"color"`
	Timestamp   string        `json:"timestamp,omitempty"`
	Image       *DiscordImage `json:"image,omitempty"`
}

type DiscordImage struct {
	URL string `json:"url"`
}

func sendDiscordWebhook(webhookURL string, pngBytes []byte, data WeatherResponse) error {
	times := data.Hourly.Time
	pressures := data.Hourly.SurfacePressure
	temps := data.Hourly.Temperature2m

	currentIdx := getCurrentIndex(times)
	currentP := pressures[currentIdx]
	currentT := temps[currentIdx]

	prev3hIdx := currentIdx - 3
	if prev3hIdx < 0 {
		prev3hIdx = 0
	}
	diff3h := currentP - pressures[prev3hIdx]

	colorInt := 0x89dceb // Cyan
	statusAlert := "🟢 安全・安定"
	if diff3h >= 3.0 || diff3h <= -3.0 {
		colorInt = 0xf38ba8 // Red
		statusAlert = fmt.Sprintf("⚠️ **急変警告** (3時間で %+.1f hPa)", diff3h)
	} else if diff3h >= 1.5 || diff3h <= -1.5 {
		colorInt = 0xf9e2af // Yellow
		statusAlert = fmt.Sprintf("🟡 **変動注意** (3時間で %+.1f hPa)", diff3h)
	}

	desc := fmt.Sprintf("• **現在気圧**: `%.1f hPa`\n• **3h 変動**: `%+.1f hPa`\n• **気温**: `%.1f °C`\n• **判定**: %s",
		currentP, diff3h, currentT, statusAlert)

	payload := DiscordWebhookPayload{
		Embeds: []DiscordEmbed{
			{
				Title:       "📊 気圧モニター",
				Description: desc,
				Color:       colorInt,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				Image: &DiscordImage{
					URL: "attachment://chart.png",
				},
			},
		},
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add payload_json part
	if err := writer.WriteField("payload_json", string(payloadJSON)); err != nil {
		return err
	}

	// Add image part
	part, err := writer.CreateFormFile("files[0]", "chart.png")
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, bytes.NewReader(pngBytes)); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord webhook error: %s (status %d)", string(respBody), resp.StatusCode)
	}

	fmt.Println("Successfully sent chart to Discord webhook.")
	return nil
}
