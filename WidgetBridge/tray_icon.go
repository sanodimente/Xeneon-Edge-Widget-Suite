package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
)

func trayIconData() []byte {
	const size = 64

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	drawHexOutline(img)
	drawCenterDot(img)

	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		log.Printf("encode tray icon: %v", err)
		return nil
	}

	return wrapPNGAsICO(size, pngData.Bytes())
}

func drawHexOutline(img *image.NRGBA) {
	stroke := color.NRGBA{R: 255, G: 208, B: 64, A: 255}
	highlight := color.NRGBA{R: 255, G: 236, B: 152, A: 110}
	points := [][2]int{{32, 8}, {50, 18}, {50, 46}, {32, 56}, {14, 46}, {14, 18}}

	for index := range points {
		next := (index + 1) % len(points)
		from := points[index]
		to := points[next]
		drawSegment(img, from[0], from[1], to[0], to[1], 4, stroke)
		drawSegment(img, from[0], from[1], to[0], to[1], 2, highlight)
	}
}

func drawCenterDot(img *image.NRGBA) {
	shadow := color.NRGBA{R: 255, G: 208, B: 64, A: 60}
	fill := color.NRGBA{R: 255, G: 216, B: 84, A: 255}
	highlight := color.NRGBA{R: 255, G: 244, B: 180, A: 180}

	drawFilledCircle(img, 32, 32, 8, shadow)
	drawFilledCircle(img, 32, 32, 5, fill)
	drawFilledCircle(img, 31, 30, 2, highlight)
}

func drawSegment(img *image.NRGBA, x0, y0, x1, y1, thickness int, fill color.NRGBA) {
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	steps := int(math.Max(math.Abs(dx), math.Abs(dy)))
	if steps == 0 {
		drawFilledCircle(img, x0, y0, thickness/2, fill)
		return
	}

	for step := 0; step <= steps; step++ {
		t := float64(step) / float64(steps)
		x := int(math.Round(float64(x0) + dx*t))
		y := int(math.Round(float64(y0) + dy*t))
		drawFilledCircle(img, x, y, thickness/2, fill)
	}
}

func drawFilledCircle(img *image.NRGBA, centerX, centerY, radius int, fill color.NRGBA) {
	radiusSquared := radius * radius
	for y := centerY - radius; y <= centerY+radius; y++ {
		for x := centerX - radius; x <= centerX+radius; x++ {
			dx := x - centerX
			dy := y - centerY
			if dx*dx+dy*dy > radiusSquared {
				continue
			}

			blendPixel(img, x, y, fill)
		}
	}
}

func blendPixel(img *image.NRGBA, x, y int, src color.NRGBA) {
	if !image.Pt(x, y).In(img.Bounds()) {
		return
	}

	dst := img.NRGBAAt(x, y)
	alpha := float64(src.A) / 255
	inverse := 1 - alpha
	img.SetNRGBA(x, y, color.NRGBA{
		R: uint8(math.Round(float64(src.R)*alpha + float64(dst.R)*inverse)),
		G: uint8(math.Round(float64(src.G)*alpha + float64(dst.G)*inverse)),
		B: uint8(math.Round(float64(src.B)*alpha + float64(dst.B)*inverse)),
		A: uint8(math.Round(float64(src.A) + float64(dst.A)*inverse)),
	})
}

func wrapPNGAsICO(size int, pngData []byte) []byte {
	if size <= 0 || size > 256 {
		panic(fmt.Sprintf("unsupported ico size %d", size))
	}

	icon := make([]byte, 22+len(pngData))
	binary.LittleEndian.PutUint16(icon[2:], 1)
	binary.LittleEndian.PutUint16(icon[4:], 1)
	if size == 256 {
		icon[6] = 0
		icon[7] = 0
	} else {
		icon[6] = byte(size)
		icon[7] = byte(size)
	}
	binary.LittleEndian.PutUint16(icon[10:], 1)
	binary.LittleEndian.PutUint16(icon[12:], 32)
	binary.LittleEndian.PutUint32(icon[14:], uint32(len(pngData)))
	binary.LittleEndian.PutUint32(icon[18:], 22)
	copy(icon[22:], pngData)

	return icon
}
