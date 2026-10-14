package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	colBg       = color.RGBA{0x1e, 0x1e, 0x2e, 0xff} // #1e1e2e (Dark Slate)
	colCard     = color.RGBA{0x2b, 0x2d, 0x3a, 0xff}
	colGrid     = color.RGBA{0x3a, 0x3d, 0x52, 0xff}
	colText     = color.RGBA{0xcd, 0xd6, 0xf4, 0xff}
	colSubText  = color.RGBA{0xa6, 0xad, 0xc8, 0xff}
	colLine     = color.RGBA{0x89, 0xdc, 0xeb, 0xff} // Cyan line
	colNowLine  = color.RGBA{0xf9, 0xe2, 0xaf, 0xff} // Yellow
	colAlertBg  = color.RGBA{0xf3, 0x8b, 0xa8, 0x40} // Light Red
	colAlertRed = color.RGBA{0xf3, 0x8b, 0xa8, 0xff} // Red
	colDot      = color.RGBA{0x89, 0xdc, 0xeb, 0xff}
)

func generateChartPNG(data WeatherResponse, before, ahead int) ([]byte, error) {
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
	n := len(subP)
	if n < 2 {
		return nil, fmt.Errorf("not enough data points")
	}

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

	// Canvas dimensions
	imgW, imgH := 800, 420
	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	draw.Draw(img, img.Bounds(), &image.Uniform{colBg}, image.Point{}, draw.Src)

	// Margins
	padL, padR, padT, padB := 70, 40, 90, 50
	chartW := imgW - padL - padR
	chartH := imgH - padT - padB

	// Chart background card
	chartRect := image.Rect(padL, padT, padL+chartW, padT+chartH)
	draw.Draw(img, chartRect, &image.Uniform{colCard}, image.Point{}, draw.Src)

	// Draw Header Info
	drawString(img, padL, 30, "ATMO : BAROMETRIC PRESSURE MONITOR", colText)
	statusText := "STABLE (安定)"
	statusCol := colLine
	if math.Abs(diff3h) >= 3.0 {
		statusText = fmt.Sprintf("CAUTION: 急変警告 (3h: %+.1f hPa)", diff3h)
		statusCol = colAlertRed
	} else if math.Abs(diff3h) >= 1.5 {
		statusText = fmt.Sprintf("NOTICE: 変動注意 (3h: %+.1f hPa)", diff3h)
		statusCol = colNowLine
	}

	infoText := fmt.Sprintf("Time: %s | Pressure: %.1f hPa | Temp: %.1f C | Status: %s",
		times[currentIdx], currentP, currentT, statusText)
	drawString(img, padL, 55, infoText, statusCol)

	// Y-axis grid and labels (4 horizontal lines)
	ySteps := 4
	for i := 0; i <= ySteps; i++ {
		yVal := maxP - (float64(i)/float64(ySteps))*pRange
		yPos := padT + int(float64(i)/float64(ySteps)*float64(chartH))
		drawHLine(img, padL, padL+chartW, yPos, colGrid)
		drawString(img, 15, yPos+4, fmt.Sprintf("%6.1f", yVal), colSubText)
	}

	// Point coordinates
	type pt struct{ x, y int }
	pts := make([]pt, n)
	nowRelIdx := currentIdx - startIdx
	nowX := 0

	for i, p := range subP {
		x := padL + int(float64(i)/float64(n-1)*float64(chartW))
		y := padT + int((maxP-p)/pRange*float64(chartH))
		pts[i] = pt{x, y}
		if i == nowRelIdx {
			nowX = x
		}
	}

	// Draw Now cursor (vertical line)
	if nowX > 0 {
		drawVLineDashed(img, nowX, padT, padT+chartH, colNowLine)
		drawString(img, nowX-12, padT-10, "NOW", colNowLine)
	}

	// Draw Curve/Line connecting points
	for i := 0; i < n-1; i++ {
		drawLine(img, pts[i].x, pts[i].y, pts[i+1].x, pts[i+1].y, colLine)
	}

	// Draw dots
	for i, pt := range pts {
		if i == nowRelIdx {
			drawCircle(img, pt.x, pt.y, 4, colNowLine)
		} else {
			drawCircle(img, pt.x, pt.y, 2, colDot)
		}
	}

	// X-axis time labels
	for i := 0; i < n; i += 4 {
		t := subTimes[i]
		h := t[len(t)-5:]
		xPos := pts[i].x
		drawString(img, xPos-15, padT+chartH+20, h, colSubText)
		drawVLine(img, xPos, padT+chartH, padT+chartH+5, colGrid)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func drawString(img *image.RGBA, x, y int, label string, col color.RGBA) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(label)
}

func drawHLine(img *image.RGBA, x1, x2, y int, col color.RGBA) {
	for x := x1; x <= x2; x++ {
		img.Set(x, y, col)
	}
}

func drawVLine(img *image.RGBA, x, y1, y2 int, col color.RGBA) {
	for y := y1; y <= y2; y++ {
		img.Set(x, y, col)
	}
}

func drawVLineDashed(img *image.RGBA, x, y1, y2 int, col color.RGBA) {
	for y := y1; y <= y2; y++ {
		if (y/4)%2 == 0 {
			img.Set(x, y, col)
		}
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, col color.RGBA) {
	dx := int(math.Abs(float64(x1 - x0)))
	dy := -int(math.Abs(float64(y1 - y0)))
	sx, sy := 1, 1
	if x0 >= x1 {
		sx = -1
	}
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, col)
		img.Set(x0, y0+1, col) // slight thickness
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func drawCircle(img *image.RGBA, cx, cy, r int, col color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				img.Set(cx+dx, cy+dy, col)
			}
		}
	}
}
