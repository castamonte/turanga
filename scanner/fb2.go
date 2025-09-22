// scanner/fb2.go
package scanner

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"turanga/config"

	"golang.org/x/text/encoding/charmap"
)

// FB2Description структура для парсинга метаданных FB2 с поддержкой пространств имён
type FB2Description struct {
	XMLName     xml.Name `xml:"http://www.gribuser.ru/xml/fictionbook/2.0 FictionBook"`
	Description struct {
		TitleInfo struct {
			BookTitle  string `xml:"book-title"`
			Annotation struct {
				Text string `xml:",innerxml"`
				P    []struct {
					Text string `xml:",chardata"`
				} `xml:"p"`
			} `xml:"annotation"`
			Authors []struct {
				FirstName  string `xml:"first-name"`
				LastName   string `xml:"last-name"`
				MiddleName string `xml:"middle-name"`
			} `xml:"author"`
			// Sequence определен как срез, так как FB2 позволяет указывать несколько серий
			Sequence []struct {
				Name   string `xml:"name,attr"`
				Number string `xml:"number,attr"`
			} `xml:"sequence"`
			Lang string `xml:"lang"`
		} `xml:"title-info"`
		PublishInfo struct {
			BookName  string `xml:"book-name"`
			Publisher string `xml:"publisher"`
			City      string `xml:"city"`
			Year      string `xml:"year"`
			ISBN      string `xml:"isbn"`
		} `xml:"publish-info"`
	} `xml:"description"`
}

// isValidUTF8 проверяет, является ли содержимое валидным UTF-8
func isValidUTF8(content []byte) bool {
	return utf8.Valid(content)
}

// containsBrokenEncoding проверяет, содержит ли текст "битые" символы кодировки
func containsBrokenEncoding(text string) bool {
	return strings.Contains(text, "Ð") ||
		strings.Contains(text, "À") ||
		strings.Contains(text, "Ñ") ||
		strings.Contains(text, "ð") ||
		strings.Contains(text, "") ||
		strings.Contains(text, "") ||
		strings.Contains(text, "Â")
}

// needsRecoding проверяет, нуждается ли файл в перекодировке
// Перекодируем только если явно указана 8-битная кодировка
func needsRecoding(content []byte) bool {
	// Если это не валидный UTF-8, проверяем заголовок
	if !isValidUTF8(content) {
		return true // Бинарные данные точно нуждаются в перекодировке
	}

	text := string(content)

	// Проверяем объявленную кодировку в XML заголовке
	// Берем первые 200 символов для поиска
	header := text
	if len(text) > 200 {
		header = text[:200]
	}

	// Ищем объявленную кодировку
	lowerHeader := strings.ToLower(header)

	// Если явно указана CP1251 или KOI8-R, перекодируем
	if strings.Contains(lowerHeader, "windows-1251") ||
		strings.Contains(lowerHeader, "cp1251") ||
		strings.Contains(lowerHeader, "koi8-r") ||
		strings.Contains(lowerHeader, "koi8r") {
		return true
	}

	// Если указана UTF-8 или кодировка не указана, не перекодируем
	if strings.Contains(lowerHeader, "utf-8") ||
		strings.Contains(lowerHeader, "utf8") ||
		!strings.Contains(header, "encoding=") {
		return false
	}

	// Для остальных случаев - проверяем "битые" символы
	return containsBrokenEncoding(text)
}

// ExtractFB2Metadata извлекает метаданные из FB2 файла с правильной обработкой кодировки
func ExtractFB2Metadata(filePath string) (author, title, annotation, isbn, year, publisher, series, seriesNumber string, err error) {
	cfg := config.GetConfig()

	// Читаем файл как байты
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return "", "", "", "", "", "", "", "", fmt.Errorf("не удалось прочитать файл: %w", err)
	}

	var xmlContent string

	// Проверяем, нуждается ли файл в перекодировке
	if needsRecoding(fileContent) {
		if cfg.Debug {
			log.Printf("Файл нуждается в перекодировке, проверяем объявленную кодировку")
		}

		text := string(fileContent)
		header := text
		if len(text) > 200 {
			header = text[:200]
		}
		lowerHeader := strings.ToLower(header)

		var success bool

		// Пробуем CP1251 если она указана
		if strings.Contains(lowerHeader, "windows-1251") ||
			strings.Contains(lowerHeader, "cp1251") {
			if cfg.Debug {
				log.Printf("Пробуем декодировать как CP1251")
			}
			reader := bytes.NewReader(fileContent)
			decoder := charmap.Windows1251.NewDecoder()
			decodedContent, decodeErr := io.ReadAll(decoder.Reader(reader))
			if decodeErr == nil {
				result := string(decodedContent)
				if isValidUTF8([]byte(result)) {
					xmlContent = result
					success = true
					if cfg.Debug {
						log.Printf("Успешно декодировано как CP1251")
					}
				}
			}
		}

		// Пробуем KOI8-R если она указана и CP1251 не сработал
		if !success && (strings.Contains(lowerHeader, "koi8-r") ||
			strings.Contains(lowerHeader, "koi8r")) {
			if cfg.Debug {
				log.Printf("Пробуем декодировать как KOI8-R")
			}
			reader := bytes.NewReader(fileContent)
			decoder := charmap.KOI8R.NewDecoder()
			decodedContent, decodeErr := io.ReadAll(decoder.Reader(reader))
			if decodeErr == nil {
				result := string(decodedContent)
				if isValidUTF8([]byte(result)) {
					xmlContent = result
					success = true
					if cfg.Debug {
						log.Printf("Успешно декодировано как KOI8-R")
					}
				}
			}
		}

		// Если ничего не сработало, используем оригинальный контент
		if !success {
			xmlContent = string(fileContent)
			if cfg.Debug {
				log.Printf("Перекодировка не удалась, используем оригинальный контент")
			}
		}
	} else {
		// Файл уже в правильной кодировке
		xmlContent = string(fileContent)
		if cfg.Debug {
			log.Printf("Файл уже в правильной кодировке (UTF-8)")
		}
	}

	// Пробуем стандартный парсинг
	decoder := xml.NewDecoder(strings.NewReader(xmlContent))
	decoder.Entity = xml.HTMLEntity

	var fb2 FB2Description
	err = decoder.Decode(&fb2)
	if err != nil {
		// Если стандартный парсинг не работает, пробуем гибкий подход
		if cfg.Debug {
			log.Printf("Ошибка стандартного парсинга FB2 метаданных: %v, пробуем гибкий парсинг", err)
		}
		return extractFB2MetadataFlexible(xmlContent)
	}

	return processFB2Metadata(fb2)
}

// processFB2Metadata обрабатывает извлеченные метаданные
func processFB2Metadata(fb2 FB2Description) (author, title, annotation, isbn, year, publisher, series, seriesNumber string, err error) {
	// Извлекаем название
	title = strings.TrimSpace(fb2.Description.TitleInfo.BookTitle)

	// Извлекаем авторов - только имя и фамилия
	var authorNames []string
	for _, auth := range fb2.Description.TitleInfo.Authors {
		firstName := strings.TrimSpace(auth.FirstName)
		lastName := strings.TrimSpace(auth.LastName)
		// ИГНОРИРУЕМ MiddleName

		// Формируем имя автора только из имени и фамилии
		var fullName string
		if firstName != "" && lastName != "" {
			fullName = firstName + " " + lastName
		} else if firstName != "" {
			fullName = firstName
		} else if lastName != "" {
			fullName = lastName
		}

		if fullName != "" {
			authorNames = append(authorNames, fullName)
		}
	}

	if len(authorNames) > 0 {
		author = strings.Join(authorNames, ", ")
	}

	// Извлекаем аннотацию
	if fb2.Description.TitleInfo.Annotation.Text != "" {
		// Очищаем HTML теги из аннотации
		annotation = cleanHTML(fb2.Description.TitleInfo.Annotation.Text)
	} else if len(fb2.Description.TitleInfo.Annotation.P) > 0 {
		var annotationParts []string
		for _, p := range fb2.Description.TitleInfo.Annotation.P {
			if strings.TrimSpace(p.Text) != "" {
				annotationParts = append(annotationParts, strings.TrimSpace(p.Text))
			}
		}
		annotation = strings.Join(annotationParts, "\n")
	}
	annotation = strings.TrimSpace(annotation)

	// Извлекаем ISBN
	isbn = strings.TrimSpace(fb2.Description.PublishInfo.ISBN)

	// Извлекаем год
	year = strings.TrimSpace(fb2.Description.PublishInfo.Year)

	// Извлекаем издателя
	publisher = strings.TrimSpace(fb2.Description.PublishInfo.Publisher)

	// Извлекаем серию и номер в серии
	if len(fb2.Description.TitleInfo.Sequence) > 0 {
		series = strings.TrimSpace(fb2.Description.TitleInfo.Sequence[0].Name)
		seriesNumber = strings.TrimSpace(fb2.Description.TitleInfo.Sequence[0].Number)
	}

	// Проверка результатов
	if title == "" {
		return "", "", "", "", "", "", "", "", fmt.Errorf("пустое название")
	}

	return author, title, annotation, isbn, year, publisher, series, seriesNumber, nil
}

// extractFB2MetadataFlexible - гибкий парсинг FB2 метаданных с регулярными выражениями
func extractFB2MetadataFlexible(xmlContent string) (author, title, annotation, isbn, year, publisher, series, seriesNumber string, err error) {
	cfg := config.GetConfig()

	// Извлекаем название книги
	titleRegex := regexp.MustCompile(`<book-title[^>]*>(.*?)</book-title>`)
	titleMatches := titleRegex.FindStringSubmatch(xmlContent)
	if len(titleMatches) > 1 {
		title = strings.TrimSpace(cleanHTML(titleMatches[1]))
	}

	// Извлекаем авторов - только имя и фамилия
	authorRegex := regexp.MustCompile(`(?s)<author[^>]*>(.*?)</author>`)
	authorBlocks := authorRegex.FindAllStringSubmatch(xmlContent, -1)

	var authorNames []string
	for _, authorBlock := range authorBlocks {
		if len(authorBlock) > 1 {
			block := authorBlock[1]

			firstNameRegex := regexp.MustCompile(`<first-name[^>]*>(.*?)</first-name>`)
			lastNameRegex := regexp.MustCompile(`<last-name[^>]*>(.*?)</last-name>`)

			firstNameMatches := firstNameRegex.FindStringSubmatch(block)
			lastNameMatches := lastNameRegex.FindStringSubmatch(block)

			var firstName, lastName string
			if len(firstNameMatches) > 1 {
				firstName = strings.TrimSpace(firstNameMatches[1])
			}
			if len(lastNameMatches) > 1 {
				lastName = strings.TrimSpace(lastNameMatches[1])
			}

			// Формируем полное имя только из имени и фамилии
			var fullName string
			if lastName != "" {
				if firstName != "" {
					fullName = firstName + " " + lastName
				} else {
					fullName = lastName
				}
			} else if firstName != "" {
				fullName = firstName
			}

			if fullName != "" {
				authorNames = append(authorNames, fullName)
			}
		}
	}

	if len(authorNames) > 0 {
		author = strings.Join(authorNames, ", ")
	}

	// Извлекаем аннотацию
	annotationRegex := regexp.MustCompile(`(?s)<annotation[^>]*>(.*?)</annotation>`)
	annotationMatches := annotationRegex.FindStringSubmatch(xmlContent)
	if len(annotationMatches) > 1 {
		annotationRaw := annotationMatches[1]
		annotation = strings.TrimSpace(cleanHTML(annotationRaw))
		if cfg.Debug {
			log.Printf("Аннотация из гибкого парсинга (до очистки): %s", annotationRaw[:min(100, len(annotationRaw))])
			log.Printf("Аннотация из гибкого парсинга (после очистки): %s", annotation[:min(100, len(annotation))])
		}
	}

	// Извлекаем ISBN
	isbnRegex := regexp.MustCompile(`<isbn[^>]*>(.*?)</isbn>`)
	isbnMatches := isbnRegex.FindStringSubmatch(xmlContent)
	if len(isbnMatches) > 1 {
		isbn = strings.TrimSpace(isbnMatches[1])
	}

	// Извлекаем год
	yearRegex := regexp.MustCompile(`<year[^>]*>(.*?)</year>`)
	yearMatches := yearRegex.FindStringSubmatch(xmlContent)
	if len(yearMatches) > 1 {
		year = strings.TrimSpace(yearMatches[1])
	}

	// Извлекаем издателя
	publisherRegex := regexp.MustCompile(`<publisher[^>]*>(.*?)</publisher>`)
	publisherMatches := publisherRegex.FindStringSubmatch(xmlContent)
	if len(publisherMatches) > 1 {
		publisher = strings.TrimSpace(publisherMatches[1])
	}

	// Извлекаем серию и номер
	sequenceRegex := regexp.MustCompile(`<sequence[^>]*name\s*=\s*["']([^"']*)["'][^>]*number\s*=\s*["']([^"']*)["'][^>]*/?>`)
	sequenceMatches := sequenceRegex.FindStringSubmatch(xmlContent)
	if len(sequenceMatches) > 2 {
		series = strings.TrimSpace(sequenceMatches[1])
		seriesNumber = strings.TrimSpace(sequenceMatches[2])
	} else {
		// Пробуем другой порядок атрибутов
		sequenceRegex = regexp.MustCompile(`<sequence[^>]*number\s*=\s*["']([^"']*)["'][^>]*name\s*=\s*["']([^"']*)["'][^>]*/?>`)
		sequenceMatches = sequenceRegex.FindStringSubmatch(xmlContent)
		if len(sequenceMatches) > 2 {
			seriesNumber = strings.TrimSpace(sequenceMatches[1])
			series = strings.TrimSpace(sequenceMatches[2])
		} else {
			// Пробуем найти только имя серии
			sequenceRegex = regexp.MustCompile(`<sequence[^>]*name\s*=\s*["']([^"']*)["'][^>]*/?>`)
			sequenceMatches = sequenceRegex.FindStringSubmatch(xmlContent)
			if len(sequenceMatches) > 1 {
				series = strings.TrimSpace(sequenceMatches[1])
			}
		}
	}

	// Проверка результатов
	if title == "" {
		return "", "", "", "", "", "", "", "", fmt.Errorf("пустое название")
	}

	if annotation != "" {
		if cfg.Debug {
			log.Printf("Гибкий парсинг: извлечена аннотация длиной %d символов", len(annotation))
		}
	}

	if cfg.Debug {
		log.Printf("Гибкий парсинг FB2 метаданных: title='%s', author='%s', series='%s', number='%s'",
			title, author, series, seriesNumber)
	}

	return author, title, annotation, isbn, year, publisher, series, seriesNumber, nil
}

// ExtractFB2ZipMetadata извлекает метаданные из FB2.ZIP файла
func ExtractFB2ZipMetadata(filePath string) (author, title, annotation, isbn, year, publisher, series, seriesNumber string, err error) {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return "", "", "", "", "", "", "", "", err
	}
	defer reader.Close()

	// Если не нашли FB2 в архиве, пробуем извлечь и обработать
	for _, file := range reader.File {
		if strings.HasSuffix(strings.ToLower(file.Name), ".fb2") {
			rc, err := file.Open()
			if err != nil {
				return "", "", "", "", "", "", "", "", err
			}

			// Создаем временный файл
			tempDir, err := os.MkdirTemp("", "fb2zip")
			if err != nil {
				rc.Close()
				return "", "", "", "", "", "", "", "", err
			}
			defer os.RemoveAll(tempDir)

			tempPath := filepath.Join(tempDir, "temp.fb2")
			tempFile, err := os.Create(tempPath)
			if err != nil {
				rc.Close()
				return "", "", "", "", "", "", "", "", err
			}

			_, err = io.Copy(tempFile, rc)
			tempFile.Close()
			rc.Close()

			if err != nil {
				return "", "", "", "", "", "", "", "", err
			}

			// Обрабатываем временный файл
			return ExtractFB2Metadata(tempPath)
		}
	}

	return "", "", "", "", "", "", "", "", fmt.Errorf("fb2 файл не найден в архиве")
}

// Простая функция для очистки HTML тегов
func cleanHTML(html string) string {
	result := ""
	tag := false
	for _, char := range html {
		if char == '<' {
			tag = true
		} else if char == '>' {
			tag = false
		} else if !tag {
			result += string(char)
		}
	}
	return strings.TrimSpace(result)
}
