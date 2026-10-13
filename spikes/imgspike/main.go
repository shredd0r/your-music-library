package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"strings"

	"golang.org/x/image/draw"
)

func protocol() string {
	if p := os.Getenv("YMLIB_IMG"); p != "" {
		return p
	}
	tp := os.Getenv("TERM_PROGRAM")
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "", os.Getenv("TERM") == "xterm-kitty", tp == "ghostty", tp == "WezTerm":
		return "kitty"
	case tp == "iTerm.app":
		return "iterm"
	}
	return "halfblock"
}

func scale(img image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}

// halfblock: each cell = upper pixel (fg) + lower pixel (bg) using '▀'
func halfblock(img image.Image, cols int) string {
	s := scale(img, cols, cols) // square image, rows = cols/2 cells
	var b strings.Builder
	for y := 0; y < cols; y += 2 {
		for x := 0; x < cols; x++ {
			r1, g1, b1, _ := s.At(x, y).RGBA()
			r2, g2, b2, _ := s.At(x, y+1).RGBA()
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", r1>>8, g1>>8, b1>>8, r2>>8, g2>>8, b2>>8)
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

func kitty(img image.Image, cols int) string {
	var buf bytes.Buffer
	png.Encode(&buf, scale(img, 400, 400))
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	var b strings.Builder
	for i := 0; i < len(data); i += 4096 {
		end := min(i+4096, len(data))
		more := 1
		if end == len(data) {
			more = 0
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Gf=100,a=T,c=%d,r=%d,m=%d;%s\x1b\\", cols, cols/2, more, data[i:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, data[i:end])
		}
	}
	return b.String() + "\n"
}

func iterm(img image.Image, cols int) string {
	var buf bytes.Buffer
	png.Encode(&buf, scale(img, 400, 400))
	return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;preserveAspectRatio=1:%s\a\n", cols, base64.StdEncoding.EncodeToString(buf.Bytes()))
}

func main() {
	path := "c.jpg"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	img, _, err := image.Decode(f)
	if err != nil {
		panic(err)
	}
	p := protocol()
	fmt.Println("renderer:", p)
	switch p {
	case "kitty":
		fmt.Print(kitty(img, 30))
	case "iterm":
		fmt.Print(iterm(img, 30))
	default:
		fmt.Print(halfblock(img, 30))
	}
}
