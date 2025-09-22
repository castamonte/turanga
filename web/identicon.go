// web/identicon.go
package web

import (
	"bytes"
	"hash"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// Constants for identicon configuration
const (
	DefaultSize       = 5
	DefaultSquareSize = 4
	DefaultBorder     = 2
	BackgroundIndex   = 0
	ForegroundIndex   = 1
)

// Color constants
var (
	DefaultBackground = color.NRGBA{0xf8, 0xf8, 0xf8, 0xff} // Светло-серый вместо бежевого
)

// Предопределенные насыщенные цвета
var vibrantColors = []color.NRGBA{
	{0xe2, 0x4e, 0x1b, 0xff}, // Ярко-оранжевый
	{0x20, 0x6f, 0xb2, 0xff}, // Ярко-синий
	{0x2e, 0x8a, 0x3e, 0xff}, // Ярко-зеленый
	{0x8e, 0x1c, 0x4a, 0xff}, // Пурпурный
	{0x5e, 0x35, 0xb1, 0xff}, // Фиолетовый
	{0xc6, 0x28, 0x28, 0xff}, // Красный
	{0x28, 0x35, 0x93, 0xff}, // Темно-синий
	{0xed, 0x6c, 0x02, 0xff}, // Оранжевый
}

// Renderer interface for generating identicons
type Renderer interface {
	Render(data []byte) []byte
	RenderString(data string) []byte
}

// IdenticonConfig holds configuration for identicon generation
type IdenticonConfig struct {
	Size       int // Size x Size grid
	SquareSize int
	Border     int
	HashFunc   func() hash.Hash64
}

// calculateDimensions calculates image dimensions based on configuration
func (c IdenticonConfig) calculateDimensions() (width, height int) {
	width = c.Size*c.SquareSize + c.Border*2
	height = c.Size*c.SquareSize + c.Border*2
	return
}

// DefaultConfig returns default identicon configuration
func DefaultConfig() IdenticonConfig {
	return IdenticonConfig{
		Size:       DefaultSize,
		SquareSize: DefaultSquareSize,
		Border:     DefaultBorder,
		HashFunc: func() hash.Hash64 {
			return xxhash.New()
		},
	}
}

// identicon implements Renderer interface
type identicon struct {
	config IdenticonConfig
	hash   hash.Hash64
	width  int
	height int
}

// New creates a new identicon renderer with default configuration
func New() Renderer {
	return NewWithConfig(DefaultConfig())
}

// NewWithConfig creates a new identicon renderer with custom configuration
func NewWithConfig(config IdenticonConfig) Renderer {
	width, height := config.calculateDimensions()
	return &identicon{
		config: config,
		hash:   config.HashFunc(),
		width:  width,
		height: height,
	}
}

// Idn creates a new identicon renderer with custom size
func Idn(size int) Renderer {
	config := DefaultConfig()
	config.Size = size
	width, height := config.calculateDimensions()
	return &identicon{
		config: config,
		hash:   config.HashFunc(),
		width:  width,
		height: height,
	}
}

// N5x5 creates a new 5x5 identicon renderer (backward compatibility)
func N5x5() Renderer {
	return New()
}

// Render generates PNG from byte data
func (icon *identicon) Render(data []byte) []byte {
	icon.hash.Reset()
	icon.hash.Write(data)
	hashValue := icon.hash.Sum64()

	// Extract vibrant color from hash
	foregroundColor := icon.extractVibrantColor(hashValue)
	hashValue >>= 24 // Shift to get pattern bits

	// Create image with palette
	palette := color.Palette{DefaultBackground, foregroundColor}
	img := image.NewPaletted(
		image.Rect(0, 0, icon.width, icon.height),
		palette,
	)

	// Generate symmetric pattern
	icon.generatePattern(img, hashValue)

	return icon.encodePNG(img)
}

// RenderString generates PNG from string data
func (icon *identicon) RenderString(data string) []byte {
	return icon.Render([]byte(data))
}

// extractVibrantColor extracts a vibrant color from hash value
func (icon *identicon) extractVibrantColor(hash uint64) color.NRGBA {
	// Используем хеш для выбора из предопределенных насыщенных цветов
	colorIndex := hash % uint64(len(vibrantColors))
	return vibrantColors[colorIndex]
}

// generatePattern creates symmetric pattern based on hash bits
func (icon *identicon) generatePattern(img *image.Paletted, patternBits uint64) {
	pixels := make([]byte, icon.config.SquareSize)
	for i := range pixels {
		pixels[i] = ForegroundIndex
	}

	sqx, sqy := 0, 0

	for i := 0; i < icon.config.Size*(icon.config.Size+1)/2; i++ {
		if patternBits&1 == 1 {
			icon.drawSquarePair(img, sqx, sqy, pixels)
		}

		patternBits >>= 1
		sqy++

		if sqy >= icon.config.Size {
			sqy = 0
			sqx++
		}
	}
}

// drawSquarePair draws symmetric pair of squares
func (icon *identicon) drawSquarePair(img *image.Paletted, sqx, sqy int, pixels []byte) {
	for i := 0; i < icon.config.SquareSize; i++ {
		// Left square
		xLeft := icon.config.Border + sqx*icon.config.SquareSize
		y := icon.config.Border + sqy*icon.config.SquareSize + i
		icon.drawPixelRow(img, xLeft, y, pixels)

		// Right square (mirrored)
		xRight := icon.config.Border + (icon.config.Size-1-sqx)*icon.config.SquareSize
		icon.drawPixelRow(img, xRight, y, pixels)
	}
}

// drawPixelRow draws a row of pixels
func (icon *identicon) drawPixelRow(img *image.Paletted, x, y int, pixels []byte) {
	if x < 0 || x >= icon.width || y < 0 || y >= icon.height {
		return // Skip out-of-bounds drawing
	}

	offset := img.PixOffset(x, y)
	copy(img.Pix[offset:], pixels)
}

// encodePNG encodes image as PNG
func (icon *identicon) encodePNG(img *image.Paletted) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Fallback: return empty PNG on error
		return []byte{}
	}
	return buf.Bytes()
}

// ShowIdenticon обрабатывает запросы на генерацию и отдачу identicon
// Это метод WebInterface, который будет использоваться как http.HandlerFunc
func (w *WebInterface) ShowIdenticon(wr http.ResponseWriter, r *http.Request) {
	// Извлекаем хеш из URL
	path := r.URL.Path
	// Простой способ извлечь хеш: убрать "/identicon/" в начале и ".png" в конце
	if len(path) <= len("/identicon/") || !strings.HasSuffix(path, ".png") {
		http.Error(wr, "Invalid identicon path", http.StatusBadRequest)
		return
	}

	// Извлекаем подстроку между "/identicon/" и ".png"
	hashStr := path[len("/identicon/") : len(path)-len(".png")]

	// Проверка: хеш должен быть непустой строкой
	if hashStr == "" {
		http.Error(wr, "Hash is required", http.StatusBadRequest)
		return
	}

	// Создаем экземпляр нашего рендерера identicon
	renderer := New() // Используем новый рендерер вместо старого N5x5()

	// Генерируем изображение PNG из хеша
	// RenderString ожидает string, поэтому используем его
	pngData := renderer.RenderString(hashStr)

	// Устанавливаем правильный Content-Type для PNG изображения
	wr.Header().Set("Content-Type", "image/png")
	// Добавим кеширование, так как identicon для одного и того же хеша всегда одинаков
	wr.Header().Set("Cache-Control", "public, max-age=31536000") // Кешировать на 1 год

	// Записываем сгенерированные PNG данные в ResponseWriter
	if _, err := wr.Write(pngData); err != nil {
		log.Printf("Ошибка отправки identicon для хеша %s: %v\n", hashStr, err)
		// На этом этапе уже невозможно отправить HTTP ошибку, так как заголовки могут быть уже отправлены
		// Лучше просто залогировать
		return
	}
	// Изображение успешно отправлено
}
