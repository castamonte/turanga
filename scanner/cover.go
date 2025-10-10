// scanner/cover.go
package scanner

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"turanga/config"

	"golang.org/x/text/encoding/charmap"

	"github.com/disintegration/imaging"
	_ "github.com/jbuchbinder/gopnm"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
)

// Структуры для парсинга content.opf в EPUB
type Package struct {
	XMLName  xml.Name `xml:"package"`
	Metadata Metadata `xml:"metadata"`
	Manifest Manifest `xml:"manifest"`
}

type Metadata struct {
	Metas []Meta `xml:"meta"`
}

type Meta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type Manifest struct {
	Items []Item `xml:"item"`
}

type Item struct {
	ID        string `xml:"id,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
}

// ExtractCover извлекает обложку из файла книги, изменяет её размер до высоты 600px и сохраняет её.
func ExtractCover(filePath, fileType string, bookID int, fileHash string) (coverURL string, err error) {
	// Определяем каталог covers - используем rootPath если доступен
	var coverDir string
	if rootPath != "" {
		// Если задан rootPath, используем его
		coverDir = filepath.Join(rootPath, "covers")
	} else {
		// Иначе используем относительный путь (старое поведение)
		coverDir = "./covers"
	}

	// Создаем каталог для обложек если его нет
	if _, err := os.Stat(coverDir); os.IsNotExist(err) {
		err = os.MkdirAll(coverDir, 0755)
		if err != nil {
			return "", err
		}
	}

	// Изменяем формат имени файла на {fileHash}.jpg
	coverFileName := fileHash + ".jpg"
	coverPath := filepath.Join(coverDir, coverFileName)

	// Проверяем, существует ли обложка уже (опционально, для избежания повторной работы)
	if _, err := os.Stat(coverPath); err == nil {
		fmt.Printf("Обложка для хеша %s уже существует: %s\n", fileHash, coverPath)
		return "/covers/" + coverFileName, nil
	} else if !os.IsNotExist(err) {
		// Другая ошибка при проверке существования
		return "", fmt.Errorf("ошибка проверки существования обложки %s: %w", coverPath, err)
	}

	// Переменная для хранения извлеченного изображения
	var img image.Image

	// Пытаемся извлечь обложку в зависимости от типа файла
	switch strings.ToLower(fileType) {
	case "epub":
		img, err = extractCoverImageFromEPUB(filePath)
	case "fb2":
		img, err = extractCoverImageFromFB2(filePath)
	case "fb2.zip":
		img, err = extractCoverImageFromFB2Zip(filePath)
	case "pdf":
		img, err = extractCoverImageFromPDF(filePath)
	case "djvu":
		img, err = extractCoverImageFromDJVU(filePath)
	default:
		// Тип файла не поддерживает извлечение обложки напрямую
		fmt.Printf("Тип файла %s не поддерживает прямое извлечение обложки\n", fileType)
		return "", nil // Нет ошибки, просто нет обложки
	}

	if err != nil {
		// Ошибка извлечения
		return "", fmt.Errorf("ошибка извлечения обложки из %s (%s): %w", filePath, fileType, err)
	}

	// Проверяем, было ли изображение извлечено
	if img == nil {
		// Изображение не было извлечено
		fmt.Printf("Изображение обложки не было извлечено для %s\n", filePath)
		return "", nil
	}

	// Всегда изменяем размер до высоты 600 пикселей, сохраняя пропорции
	targetHeight := 600
	img = imaging.Resize(img, 0, targetHeight, imaging.Lanczos) // Используем качественный фильтр
	if cfg.Debug {
		log.Printf("Обложка изменена до высоты %dpx\n", targetHeight)
	}

	// Сохранение в JPEG
	f, err := os.Create(coverPath)
	if err != nil {
		return "", fmt.Errorf("не удалось создать файл обложки %s: %w", coverPath, err)
	}
	defer f.Close()

	// Кодируем и сохраняем изображение как JPEG с качеством 85
	err = imaging.Encode(f, img, imaging.JPEG, imaging.JPEGQuality(85))
	if err != nil {
		// Если сохранение не удалось, удаляем частично созданный файл
		_ = os.Remove(coverPath)
		return "", fmt.Errorf("не удалось сохранить обложку в файл %s: %w", coverPath, err)
	}

	if cfg.Debug {
		log.Printf("Обложка извлечена, изменена и сохранена: %s\n", coverPath)
	}
	return "/covers/" + coverFileName, nil
}

// extractCoverImageFromEPUB извлекает обложку из EPUB файла и возвращает image.Image
func extractCoverImageFromEPUB(filePath string) (image.Image, error) {
	cfg := config.GetConfig()

	// Открываем EPUB файл как zip архив
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть EPUB как zip архив: %w", err)
	}
	defer reader.Close()

	// Ищем content.opf
	var contentOpfFile *zip.File
	for _, f := range reader.File {
		// Учитываем разные возможные пути к content.opf
		// Стандартный путь: OEBPS/content.opf
		// Но может быть и просто content.opf в корне
		// Или в другой папке. Обычно он упоминается в mimetype container.
		// Для простоты ищем по имени файла.
		lowerName := strings.ToLower(f.Name)
		if strings.HasSuffix(lowerName, "content.opf") {
			contentOpfFile = f
			break
		}
	}

	if contentOpfFile == nil {
		// Если content.opf не найден, пробуем старый метод
		if cfg.Debug {
			log.Println("content.opf не найден, пробуем старый метод")
		}
		return extractCoverImageFromEPUBFallback(filePath)
	}

	// Читаем content.opf
	opfReader, err := contentOpfFile.Open()
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть content.opf: %w", err)
	}
	defer opfReader.Close()

	// Парсим content.opf
	var pkg Package
	decoder := xml.NewDecoder(opfReader)
	err = decoder.Decode(&pkg)
	if err != nil {
		// Если парсинг не удался, пробуем старый метод
		if cfg.Debug {
			log.Printf("Ошибка парсинга content.opf: %v, пробуем старый метод", err)
		}
		return extractCoverImageFromEPUBFallback(filePath)
	}

	// Ищем meta с name="cover"
	var coverID string
	for _, meta := range pkg.Metadata.Metas {
		if meta.Name == "cover" {
			coverID = meta.Content
			break
		}
	}

	if coverID == "" {
		// Если meta cover не найден, пробуем старый метод
		if cfg.Debug {
			log.Println("meta cover не найден, пробуем старый метод")
		}
		return extractCoverImageFromEPUBFallback(filePath)
	}

	// Ищем item в manifest с id равным coverID
	var coverItem *Item
	for _, item := range pkg.Manifest.Items {
		if item.ID == coverID {
			// Проверяем, является ли он изображением
			if strings.HasPrefix(item.MediaType, "image/") {
				coverItem = &item
				break
			}
		}
	}

	if coverItem == nil {
		// Если item не найден или не изображение, пробуем старый метод
		if cfg.Debug {
			log.Printf("Item с id=%s не найден как изображение, пробуем старый метод", coverID)
		}
		return extractCoverImageFromEPUBFallback(filePath)
	}

	// coverItem.Href содержит путь к файлу обложки относительно папки content.opf
	// Нужно построить полный путь внутри архива
	// contentOpfFile.Name это путь к content.opf, например "OEBPS/content.opf"
	// coverItem.Href это путь от папки content.opf, например "Images/cover.jpg"
	// Полный путь в архиве: "OEBPS/Images/cover.jpg"
	contentOpfDir := filepath.Dir(contentOpfFile.Name)
	coverImagePath := filepath.Join(contentOpfDir, coverItem.Href)
	// Нормализуем путь для поиска в zip архиве (внутри zip пути разделены "/")
	coverImagePath = filepath.ToSlash(coverImagePath)

	// Ищем файл обложки в архиве
	var coverImageFile *zip.File
	for _, f := range reader.File {
		// Сравниваем нормализованные пути
		if filepath.ToSlash(f.Name) == coverImagePath {
			coverImageFile = f
			break
		}
	}

	if coverImageFile == nil {
		// Если файл не найден по точному пути, пробуем найти по имени файла из Href
		// Это может помочь, если пути немного отличаются
		coverImageName := filepath.Base(coverItem.Href)
		for _, f := range reader.File {
			if strings.EqualFold(filepath.Base(f.Name), coverImageName) {
				coverImageFile = f
				if cfg.Debug {
					log.Printf("Найден файл обложки по имени: %s (был искать %s)", f.Name, coverImagePath)
				}
				break
			}
		}
	}

	if coverImageFile == nil {
		// Если файл обложки не найден, пробуем старый метод
		if cfg.Debug {
			log.Printf("Файл обложки %s не найден в архиве, пробуем старый метод", coverImagePath)
		}
		return extractCoverImageFromEPUBFallback(filePath)
	}

	// Открываем и декодируем файл обложки
	coverReader, err := coverImageFile.Open()
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл обложки %s: %w", coverImageFile.Name, err)
	}
	defer coverReader.Close()

	img, _, err := image.Decode(coverReader)
	if err != nil {
		// Пробуем старый метод, если декодирование не удалось
		if cfg.Debug {
			log.Printf("Не удалось декодировать обложку %s: %v, пробуем старый метод", coverImageFile.Name, err)
		}
		return extractCoverImageFromEPUBFallback(filePath)
	}

	if cfg.Debug {
		log.Printf("Обложка успешно извлечена из EPUB по meta cover: %s", coverImageFile.Name)
	}
	return img, nil
}

// extractCoverImageFromEPUBFallback - старая реализация как запасной вариант
func extractCoverImageFromEPUBFallback(filePath string) (image.Image, error) {
	// Проверяем наличие необходимых утилит
	_, err := exec.LookPath("unzip")
	if err != nil {
		return nil, fmt.Errorf("unzip не найден")
	}
	// Ищем файлы изображений в EPUB
	cmd := exec.Command("unzip", "-l", filePath)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	outputStr := string(output)
	var imageFiles []string
	// Ищем потенциальные файлы обложек
	for _, line := range strings.Split(outputStr, "\n") {
		if strings.Contains(line, ".jpg") || strings.Contains(line, ".jpeg") || strings.Contains(line, ".png") {
			parts := strings.Fields(line)
			if len(parts) > 3 {
				filename := parts[len(parts)-1]
				// Ищем файлы с именами, похожими на обложки
				lowerName := strings.ToLower(filename)
				if strings.Contains(lowerName, "cover") ||
					strings.Contains(lowerName, "обложка") ||
					strings.Contains(lowerName, "thumbnail") ||
					filename == "cover.jpg" ||
					filename == "cover.jpeg" ||
					filename == "cover.png" ||
					filename == "images/cover.jpg" {
					imageFiles = append(imageFiles, filename)
					break // Берем первый подходящий файл
				}
			}
		}
	}
	// Если не нашли явных обложек, берем первый попавшийся jpg/png
	if len(imageFiles) == 0 {
		for _, line := range strings.Split(outputStr, "\n") {
			if strings.Contains(line, ".jpg") || strings.Contains(line, ".jpeg") || strings.Contains(line, ".png") {
				parts := strings.Fields(line)
				if len(parts) > 3 {
					filename := parts[len(parts)-1]
					imageFiles = append(imageFiles, filename)
					break
				}
			}
		}
	}
	// Извлекаем найденный файл обложки
	if len(imageFiles) > 0 {
		cmd = exec.Command("unzip", "-p", filePath, imageFiles[0])
		output, err = cmd.Output()
		if err == nil && len(output) > 0 {
			// Декодируем изображение из байтов
			img, _, decodeErr := image.Decode(strings.NewReader(string(output)))
			if decodeErr != nil {
				return nil, fmt.Errorf("не удалось декодировать изображение из EPUB (fallback): %w", decodeErr)
			}
			return img, nil
		}
	}
	return nil, fmt.Errorf("обложка не найдена в EPUB (fallback)")
}

// FB2 структура для извлечения обложки с поддержкой пространств имён
type FB2 struct {
	XMLName     xml.Name `xml:"http://www.gribuser.ru/xml/fictionbook/2.0 FictionBook"`
	Description struct {
		TitleInfo struct {
			Coverpage struct {
				Image struct {
					Href  string `xml:"href,attr"`
					LHref string `xml:"href,attr xmlns:l http://www.w3.org/1999/xlink"`
				} `xml:"image"`
			} `xml:"coverpage"`
		} `xml:"title-info"`
	} `xml:"description"`
	Binary []struct {
		ID          string `xml:"id,attr"`
		ContentType string `xml:"content-type,attr"`
		Data        string `xml:",chardata"`
	} `xml:"binary"`
}

// extractCoverImageFromFB2 извлекает обложку из FB2 файла и возвращает image.Image
func extractCoverImageFromFB2(filePath string) (image.Image, error) {
	cfg := config.GetConfig()

	// Сначала читаем файл для проверки кодировки
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать файл: %w", err)
	}

	// Проверяем кодировку (часто FB2 в windows-1251)
	var xmlContent []byte
	if strings.Contains(string(fileContent[:100]), "windows-1251") {
		// Конвертируем из windows-1251 в UTF-8
		reader := strings.NewReader(string(fileContent))
		decoder := charmap.Windows1251.NewDecoder()
		utf8Content, err := io.ReadAll(decoder.Reader(reader))
		if err != nil {
			if cfg.Debug {
				log.Printf("Предупреждение: не удалось конвертировать из windows-1251: %v", err)
			}
			xmlContent = fileContent // используем оригинальный контент
		} else {
			xmlContent = utf8Content
		}
	} else {
		xmlContent = fileContent
	}

	// Парсим XML
	decoder := xml.NewDecoder(strings.NewReader(string(xmlContent)))
	decoder.Entity = xml.HTMLEntity

	var fb2 FB2
	err = decoder.Decode(&fb2)
	if err != nil {
		// Если стандартный парсинг не работает, пробуем более гибкий подход
		if cfg.Debug {
			log.Printf("Ошибка стандартного парсинга FB2: %v, пробуем гибкий парсинг", err)
		}
		return extractCoverImageFromFB2Flexible(string(xmlContent))
	}

	// Проверяем, есть ли ссылка на обложку (разные варианты)
	coverHref := fb2.Description.TitleInfo.Coverpage.Image.Href
	if coverHref == "" {
		coverHref = fb2.Description.TitleInfo.Coverpage.Image.LHref
	}

	if coverHref == "" {
		return nil, fmt.Errorf("обложка не найдена в FB2 (пустая ссылка)")
	}

	// Извлекаем ID из href (может быть с # или без)
	coverID := strings.TrimPrefix(coverHref, "#")
	if coverID == "" {
		return nil, fmt.Errorf("некорректный ID обложки в href: %s", coverHref)
	}

	if cfg.Debug {
		log.Printf("Ищем обложку с ID: %s", coverID)
	}

	// Ищем binary данные с таким ID (более гибкий поиск)
	for i, binary := range fb2.Binary {
		if cfg.Debug {
			log.Printf("Проверяем binary[%d]: ID='%s', ContentType='%s'", i, binary.ID, binary.ContentType)
		}

		// Проверяем несколько условий
		if (binary.ID == coverID ||
			strings.EqualFold(binary.ID, coverID)) &&
			(strings.Contains(strings.ToLower(binary.ContentType), "image") ||
				strings.Contains(strings.ToLower(binary.ContentType), "jpeg") ||
				strings.Contains(strings.ToLower(binary.ContentType), "png") ||
				strings.Contains(strings.ToLower(binary.ContentType), "gif") ||
				strings.Contains(strings.ToLower(binary.ContentType), "jpg")) {

			// Очищаем данные от пробелов и переносов строк
			cleanData := strings.TrimSpace(binary.Data)
			cleanData = strings.ReplaceAll(cleanData, "\n", "")
			cleanData = strings.ReplaceAll(cleanData, "\r", "")
			cleanData = strings.ReplaceAll(cleanData, " ", "")
			cleanData = strings.ReplaceAll(cleanData, "\t", "")

			if cleanData == "" {
				return nil, fmt.Errorf("данные обложки пустые для ID: %s", coverID)
			}

			if cfg.Debug {
				log.Printf("Найдены данные обложки, длина: %d символов", len(cleanData))
			}

			// Декодируем base64 данные
			imageData, err := base64.StdEncoding.DecodeString(cleanData)
			if err != nil {
				// Пробуем с игнорированием пробелов (иногда в base64 есть пробелы)
				cleanDataNoSpaces := strings.Map(func(r rune) rune {
					if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
						return -1
					}
					return r
				}, cleanData)

				imageData, err = base64.StdEncoding.DecodeString(cleanDataNoSpaces)
				if err != nil {
					// Пробуем URL-encoding
					imageData, err = base64.URLEncoding.DecodeString(cleanData)
					if err != nil {
						return nil, fmt.Errorf("ошибка декодирования base64 для ID %s: %w", coverID, err)
					}
				}
			}

			if len(imageData) == 0 {
				return nil, fmt.Errorf("декодированные данные обложки пустые для ID: %s", coverID)
			}

			if cfg.Debug {
				log.Printf("Декодированы данные обложки, размер: %d байт", len(imageData))
			}

			// Декодируем изображение из байтов
			img, format, decodeErr := image.Decode(bytes.NewReader(imageData))
			if decodeErr != nil {
				// Попробуем определить формат вручную
				if len(imageData) > 4 {
					header := imageData[:4]
					if cfg.Debug {
						log.Printf("Заголовок изображения (hex): %x", header)
					}
				}
				return nil, fmt.Errorf("не удалось декодировать изображение из FB2 (формат: %s): %w", format, decodeErr)
			}

			if cfg.Debug {
				log.Printf("Успешно извлечена обложка из FB2, формат: %s", format)
			}
			return img, nil
		}
	}

	return nil, fmt.Errorf("данные обложки не найдены в FB2 для ID: %s, всего binary элементов: %d", coverID, len(fb2.Binary))
}

// extractCoverImageFromFB2Flexible - гибкий парсинг FB2 с регулярными выражениями для обложки
func extractCoverImageFromFB2Flexible(xmlContent string) (image.Image, error) {
	cfg := config.GetConfig()

	// Конвертируем XML контент с автоопределением кодировки
	processedContent := xmlContent

	// Проверяем объявленную кодировку
	if strings.Contains(xmlContent, "windows-1251") {
		// Пробуем декодировать как CP1251
		reader := strings.NewReader(xmlContent)
		decoder := charmap.Windows1251.NewDecoder()
		utf8Content, decodeErr := io.ReadAll(decoder.Reader(reader))
		if decodeErr == nil {
			processedContent = string(utf8Content)
		} else {
			if cfg.Debug {
				log.Printf("Предупреждение: не удалось декодировать из windows-1251: %v", decodeErr)
			}
		}
	} else if strings.Contains(xmlContent, "koi8-r") {
		// Пробуем декодировать как KOI8-R
		reader := strings.NewReader(xmlContent)
		decoder := charmap.KOI8R.NewDecoder()
		utf8Content, decodeErr := io.ReadAll(decoder.Reader(reader))
		if decodeErr == nil {
			processedContent = string(utf8Content)
		} else {
			if cfg.Debug {
				log.Printf("Предупреждение: не удалось декодировать из koi8-r: %v", decodeErr)
			}
		}
	}

	// Ищем coverpage с более гибким регулярным выражением
	coverpageRegex := regexp.MustCompile(`(?s)<coverpage[^>]*>.*?(?:<image[^>]*href\s*=\s*["']([^"']+)["'][^>]*/?|<image[^>]*l:href\s*=\s*["']([^"']+)["'][^>]*/?).*?</coverpage>`)
	coverpageMatches := coverpageRegex.FindStringSubmatch(processedContent)

	var coverHref string
	if len(coverpageMatches) >= 2 {
		// Проверяем оба варианта (href и l:href)
		if coverpageMatches[1] != "" {
			coverHref = coverpageMatches[1]
		} else if coverpageMatches[2] != "" {
			coverHref = coverpageMatches[2]
		}
	}

	if coverHref == "" {
		// Пробуем более простой вариант
		simpleRegex := regexp.MustCompile(`<image[^>]*l:href\s*=\s*["']([^"']+)["'][^>]*/?>`)
		simpleMatches := simpleRegex.FindStringSubmatch(processedContent)
		if len(simpleMatches) > 1 {
			coverHref = simpleMatches[1]
		} else {
			// Пробуем href без namespace
			hrefRegex := regexp.MustCompile(`<image[^>]*href\s*=\s*["']([^"']+)["'][^>]*/?>`)
			hrefMatches := hrefRegex.FindStringSubmatch(processedContent)
			if len(hrefMatches) > 1 {
				coverHref = hrefMatches[1]
			}
		}
	}

	if coverHref == "" {
		return nil, fmt.Errorf("обложка не найдена в FB2 (гибкий парсинг)")
	}

	coverID := strings.TrimPrefix(coverHref, "#")

	if coverID == "" {
		return nil, fmt.Errorf("некорректный ID обложки: %s", coverHref)
	}

	if cfg.Debug {
		log.Printf("Гибкий парсинг: найден ID обложки: %s", coverID)
	}

	// Ищем binary данные с таким ID
	binaryRegex := regexp.MustCompile(fmt.Sprintf(
		`(?s)<binary[^>]*id\s*=\s*["']%s["'][^>]*content-type\s*=\s*["']([^"']*)["'][^>]*>(.*?)</binary>`,
		regexp.QuoteMeta(coverID)))

	binaryMatches := binaryRegex.FindStringSubmatch(processedContent)

	var imageDataStr string

	if len(binaryMatches) >= 3 {
		// Нашли с content-type
		imageDataStr = binaryMatches[2]
	} else {
		// Пробуем другой порядок атрибутов
		binaryRegex = regexp.MustCompile(fmt.Sprintf(
			`(?s)<binary[^>]*content-type\s*=\s*["']([^"']*)["'][^>]*id\s*=\s*["']%s["'][^>]*>(.*?)</binary>`,
			regexp.QuoteMeta(coverID)))

		binaryMatches = binaryRegex.FindStringSubmatch(processedContent)

		if len(binaryMatches) >= 3 {
			imageDataStr = binaryMatches[2]
		} else {
			// Пробуем найти binary без строгого порядка атрибутов
			generalBinaryRegex := regexp.MustCompile(fmt.Sprintf(
				`(?s)<binary[^>]*id\s*=\s*["']%s["'][^>]*>(.*?)</binary>`,
				regexp.QuoteMeta(coverID)))

			binaryMatches = generalBinaryRegex.FindStringSubmatch(processedContent)

			if len(binaryMatches) >= 2 {
				imageDataStr = binaryMatches[1]
			} else {
				return nil, fmt.Errorf("данные обложки не найдены для ID: %s", coverID)
			}
		}
	}

	if cfg.Debug {
		log.Printf("Гибкий парсинг: найдены данные обложки")
	}

	// Очищаем данные
	cleanData := strings.TrimSpace(imageDataStr)
	cleanData = regexp.MustCompile(`\s+`).ReplaceAllString(cleanData, "")

	if cleanData == "" {
		return nil, fmt.Errorf("данные обложки пустые")
	}

	// Декодируем base64
	imageData, err := base64.StdEncoding.DecodeString(cleanData)
	if err != nil {
		// Пробуем с игнорированием пробелов
		cleanDataNoSpaces := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, cleanData)

		imageData, err = base64.StdEncoding.DecodeString(cleanDataNoSpaces)
		if err != nil {
			return nil, fmt.Errorf("ошибка декодирования base64: %w", err)
		}
	}

	if len(imageData) == 0 {
		return nil, fmt.Errorf("декодированные данные пустые")
	}

	// Декодируем изображение
	img, format, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, fmt.Errorf("не удалось декодировать изображение (формат: %s): %w", format, err)
	}

	if cfg.Debug {
		log.Printf("Гибкий парсинг: успешно извлечена обложка, формат: %s", format)
	}
	return img, nil
}

// extractCoverImageFromFB2Zip извлекает обложку из FB2.ZIP файла и возвращает image.Image
func extractCoverImageFromFB2Zip(filePath string) (image.Image, error) {
	cfg := config.GetConfig()

	// Проверяем наличие unzip
	_, err := exec.LookPath("unzip")
	if err != nil {
		return nil, fmt.Errorf("unzip не найден")
	}

	// Создаем временный каталог для извлечения всего архива
	tempDir, err := os.MkdirTemp("", "fb2_extract_full")
	if err != nil {
		return nil, fmt.Errorf("не удалось создать временный каталог: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Извлекаем весь архив
	if cfg.Debug {
		log.Printf("DEBUG (extractCoverImageFromFB2Zip): Извлекаю весь архив %s в %s", filePath, tempDir)
	}
	cmd := exec.Command("unzip", "-o", filePath, "-d", tempDir)
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("ошибка извлечения архива: %w", err)
	}

	// Ищем FB2 файл в извлеченном каталоге
	var fb2FilePath string
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".fb2") {
			fb2FilePath = path
			return filepath.SkipDir // Найден первый FB2 файл, прекращаем поиск
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("ошибка поиска FB2 файла в извлеченном архиве: %w", err)
	}

	if fb2FilePath == "" {
		return nil, fmt.Errorf("FB2 файл не найден в архиве после извлечения")
	}

	if cfg.Debug {
		log.Printf("DEBUG (extractCoverImageFromFB2Zip): Найден FB2 файл: %s", fb2FilePath)
	}

	// Извлекаем обложку из FB2 файла
	img, err := extractCoverImageFromFB2(fb2FilePath)
	if err != nil {
		return nil, fmt.Errorf("ошибка извлечения обложки из FB2: %w", err)
	}

	return img, nil
}

// extractCoverImageFromPDF извлекает обложку из PDF файла и возвращает image.Image
func extractCoverImageFromPDF(filePath string) (image.Image, error) {
	// Проверяем наличие pdftoppm
	_, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, fmt.Errorf("pdftoppm не найден")
	}

	// Создаем временный каталог для извлечения
	tempDir, err := os.MkdirTemp("", "pdf_cover")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir) // Удаляем временный каталог после использования

	// Формируем путь для выходного файла. pdftoppm добавит -1 к имени.
	// Мы указываем просто базовое имя, например "cover", и pdftoppm создаст "cover-1.jpg"
	outputBasePath := filepath.Join(tempDir, "cover")

	// Извлекаем первую страницу как изображение JPEG с высоким разрешением
	// -jpeg: Формат вывода JPEG
	// -f 1: Первая страница
	// -l 1: Только одна страница
	// -scale-to 2000: Масштабируем до ширины 2000 пикселей (или высоты, смотря что больше), сохраняя пропорции
	//                 Это даст хорошее качество для последующего изменения размера до 600px по высоте.
	cmd := exec.Command("pdftoppm", "-jpeg", "-f", "1", "-l", "1", "-scale-to", "2000", filePath, outputBasePath)
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("pdftoppm ошибка: %w (команда: %v)", err, cmd.Args)
	}

	// pdftoppm создает файл с суффиксом -1, например, cover-1.jpg
	expectedCoverPath := outputBasePath + "-1.jpg"

	// Проверяем, что файл JPEG создан
	if _, err := os.Stat(expectedCoverPath); os.IsNotExist(err) {
		// Если файл с -1.jpg не найден, попробуем найти любой .jpg файл в tempDir
		// Это может помочь, если pdftoppm ведет себя немного иначе
		jpgFiles, _ := filepath.Glob(filepath.Join(tempDir, "*.jpg"))
		if len(jpgFiles) > 0 {
			expectedCoverPath = jpgFiles[0] // Берем первый найденный JPG
		} else {
			return nil, fmt.Errorf("файл обложки JPEG не создан pdftoppm по пути %s и не найден в %s", expectedCoverPath, tempDir)
		}
	}

	// Открываем созданный JPEG файл и декодируем его
	// Убедитесь, что импортирован _ "image/jpeg"
	file, err := os.Open(expectedCoverPath)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть временный файл обложки %s: %w", expectedCoverPath, err)
	}
	defer file.Close()

	img, format, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("не удалось декодировать изображение из PDF (формат: %s, файл: %s): %w", format, expectedCoverPath, err)
	}

	return img, nil
}

func extractCoverImageFromDJVU(filePath string) (image.Image, error) {
	cfg := config.GetConfig()

	// Проверяем наличие необходимых утилит
	ddjvuPath, err := exec.LookPath("ddjvu")
	if err != nil {
		return nil, fmt.Errorf("ddjvu не найден: %w", err)
	}

	// Создаем временный каталог для извлечения
	tempDir, err := os.MkdirTemp("", "djvu_cover")
	if err != nil {
		return nil, fmt.Errorf("не удалось создать временный каталог: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Пробуем только допустимые форматы согласно сообщению об ошибке
	approaches := []struct {
		name   string
		format string
	}{
		{"PPM формат", "ppm"},
		{"TIFF формат", "tiff"},
		{"PNG формат", "ppm"}, // Будем конвертировать PPM в PNG
		{"Без указания формата (по умолчанию PPM)", ""},
	}

	var lastError error

	for i, approach := range approaches {
		if cfg.Debug {
			log.Printf("DJVU попытка %d: %s", i+1, approach.name)
		}

		var cmdArgs []string
		tempOutput := ""

		if approach.format != "" && approach.format != "ppm" {
			// Для форматов кроме PPM
			tempOutput = filepath.Join(tempDir, fmt.Sprintf("cover_%d.%s", i, approach.format))
			cmdArgs = []string{ddjvuPath, "--page=1", fmt.Sprintf("--format=%s", approach.format), filePath, tempOutput}
		} else if approach.format == "ppm" {
			// Для PPM формата
			tempOutput = filepath.Join(tempDir, fmt.Sprintf("cover_%d.ppm", i))
			cmdArgs = []string{ddjvuPath, "--page=1", "--format=ppm", filePath, tempOutput}
		} else {
			// Без указания формата (по умолчанию PPM)
			tempOutput = filepath.Join(tempDir, fmt.Sprintf("cover_%d.ppm", i))
			cmdArgs = []string{ddjvuPath, "--page=1", filePath, tempOutput}
		}

		if cfg.Debug {
			log.Printf("Команда: %v", cmdArgs)
		}

		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		output, err := cmd.CombinedOutput()

		if err != nil {
			if cfg.Debug {
				log.Printf("DJVU попытка %d (%s) не удалась: %v, вывод: %s", i+1, approach.name, err, string(output))
			}
			lastError = fmt.Errorf("попытка %d (%s): %w, вывод: %s", i+1, approach.name, err, string(output))
			continue
		}

		// Проверяем, что файл создан и не пустой
		fileInfo, err := os.Stat(tempOutput)
		if err != nil {
			if cfg.Debug {
				log.Printf("DJVU попытка %d: файл %s не создан: %v", i+1, tempOutput, err)
			}
			lastError = fmt.Errorf("файл не создан: %w", err)
			continue
		}

		if fileInfo.Size() == 0 {
			if cfg.Debug {
				log.Printf("DJVU попытка %d: файл %s пустой", i+1, tempOutput)
			}
			lastError = fmt.Errorf("файл пустой")
			continue
		}

		if cfg.Debug {
			log.Printf("DJVU попытка %d успешна: создан файл %s, размер: %d байт", i+1, tempOutput, fileInfo.Size())
		}

		// Пробуем декодировать изображение
		img, err := decodeImageFile(tempOutput, cfg.Debug)
		if err != nil {
			if cfg.Debug {
				log.Printf("DJVU попытка %d: не удалось декодировать изображение: %v", i+1, err)
			}
			lastError = fmt.Errorf("декодирование: %w", err)
			continue
		}

		if cfg.Debug {
			log.Printf("DJVU попытка %d: успешно извлечена обложка", i+1)
		}
		return img, nil
	}

	// Если все попытки неудачны, пробуем djvudump для диагностики
	if cfg.Debug {
		diagnoseDJVUFile(filePath)
	}

	return nil, fmt.Errorf("не удалось извлечь обложку из DJVU файла, последняя ошибка: %w", lastError)
}

// Вспомогательная функция для декодирования изображения
func decodeImageFile(filePath string, debug bool) (image.Image, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл: %w", err)
	}
	defer file.Close()

	// Проверяем сигнатуру файла
	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать заголовок файла: %w", err)
	}

	contentType := http.DetectContentType(buffer)
	if debug {
		log.Printf("Тип контента файла %s: %s", filePath, contentType)
	}

	// Возвращаемся в начало файла
	_, err = file.Seek(0, 0)
	if err != nil {
		return nil, fmt.Errorf("не удалось перемотать файл: %w", err)
	}

	// Пробуем стандартное декодирование
	img, format, err := image.Decode(file)
	if err == nil {
		if debug {
			log.Printf("Успешно декодировано стандартным decoder'ом, формат: %s", format)
		}
		return img, nil
	}

	if debug {
		log.Printf("Стандартное декодирование не удалось: %v", err)
	}

	// Пробуем альтернативные методы декодирования
	file.Seek(0, 0) // Снова в начало

	// Для PPM файлов пробуем специальный подход
	if strings.HasSuffix(strings.ToLower(filePath), ".ppm") {
		img, err = imaging.Decode(file)
		if err == nil {
			if debug {
				log.Printf("Успешно декодировано как PPM через imaging")
			}
			return img, nil
		}
		if debug {
			log.Printf("PPM декодирование через imaging не удалось: %v", err)
		}
	}

	return nil, fmt.Errorf("не удалось декодировать изображение: %w", err)
}

// Функция диагностики DJVU файла
func diagnoseDJVUFile(filePath string) {
	if djvudumpPath, err := exec.LookPath("djvudump"); err == nil {
		cmd := exec.Command(djvudumpPath, filePath)
		output, err := cmd.CombinedOutput()
		if err == nil {
			if cfg.Debug {
				log.Printf("DJVU диагностика (djvudump): %s", string(output))
			}
		} else {
			if cfg.Debug {
				log.Printf("DJVU диагностика не удалась: %v, вывод: %s", err, string(output))
			}
		}
	}

	// Также пробуем djvused для получения метаданных
	if djvusedPath, err := exec.LookPath("djvused"); err == nil {
		cmd := exec.Command(djvusedPath, "-e", "print-outline", filePath)
		output, err := cmd.CombinedOutput()
		if err == nil {
			log.Printf("DJVU структура: %s", string(output))
		}
	}
}

// ProcessAndSaveCoverImage принимает image.Image, изменяет его размер и сохраняет в файл с именем {fileHash}.jpg.
// Возвращает URL сохраненной обложки или ошибку.
func ProcessAndSaveCoverImage(img image.Image, fileHash string) (coverURL string, err error) {
	// Определяем каталог covers - используем rootPath если доступен
	var coverDir string
	if rootPath != "" { // <-- Использует глобальную переменную rootPath пакета scanner
		// Если задан rootPath, используем его
		coverDir = filepath.Join(rootPath, "covers")
	} else {
		// Иначе используем относительный путь (старое поведение)
		coverDir = "./covers"
	}

	// Создаем каталог для обложек если его нет
	if _, err := os.Stat(coverDir); os.IsNotExist(err) {
		err = os.MkdirAll(coverDir, 0755)
		if err != nil {
			return "", fmt.Errorf("не удалось создать каталог обложек: %w", err)
		}
	}

	// Формируем имя файла обложки: {fileHash}.jpg
	coverFileName := fileHash + ".jpg"
	coverPath := filepath.Join(coverDir, coverFileName)

	// Изменяем размер до высоты 600 пикселей, сохраняя пропорции
	// Это должно совпадать с логикой в ExtractCover
	targetHeight := 600
	resizedImg := imaging.Resize(img, 0, targetHeight, imaging.Lanczos) // Используем качественный фильтр
	fmt.Printf("Загруженная обложка изменена до высоты %dpx\n", targetHeight)

	// Открываем файл для записи
	f, err := os.Create(coverPath)
	if err != nil {
		return "", fmt.Errorf("не удалось создать файл обложки %s: %w", coverPath, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("ошибка закрытия файла обложки: %w", closeErr)
		}
	}()

	// Кодируем и сохраняем изображение как JPEG с качеством 85
	encodeErr := imaging.Encode(f, resizedImg, imaging.JPEG, imaging.JPEGQuality(85))
	if encodeErr != nil {
		// Если сохранение не удалось, пытаемся удалить частично созданный файл
		_ = os.Remove(coverPath)
		return "", fmt.Errorf("не удалось сохранить обложку в файл %s: %w", coverPath, encodeErr)
	}

	// Обложка успешно обработана и сохранена
	fmt.Printf("Загруженная обложка обработана и сохранена: %s\n", coverPath)
	return "/covers/" + coverFileName, nil
}
