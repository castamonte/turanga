// scanner/epub.go
package scanner

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
)

// EPUBContainer структура для парсинга container.xml
type EPUBContainer struct {
	Rootfiles []struct {
		Path string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

// EPUBPackage структура для парсинга .opf файла с расширенными метаданными
// Обновлена для извлечения серии и номера серии
type EPUBPackage struct {
	Metadata struct {
		Title    []string `xml:"title"`
		Creators []struct {
			Text string `xml:",chardata"`
		} `xml:"creator"`
		Description []struct {
			Text string `xml:",chardata"`
		} `xml:"description"`
		Identifiers []struct {
			Text   string `xml:",chardata"`
			ID     string `xml:"id,attr"`
			Scheme string `xml:"scheme,attr"`
		} `xml:"identifier"`
		Date []struct {
			Text string `xml:",chardata"`
		} `xml:"date"`
		Publisher []struct {
			Text string `xml:",chardata"`
		} `xml:"publisher"`
		// Для извлечения серии и номера серии из Dublin Core или кастомных мета-тегов
		// Используем более гибкий подход с Meta
		Meta []struct {
			Name    string `xml:"name,attr,omitempty"`
			Content string `xml:"content,attr,omitempty"`
			Text    string `xml:",chardata"` // Для тегов вида <meta property="...">value</meta>
			// Для тегов вида <meta property="...">value</meta>
			Property string `xml:"property,attr,omitempty"`
		} `xml:"meta"`
		// Старый способ, оставлен для совместимости с простыми тегами <meta>
		// Series []struct {
		// 	Text string `xml:",chardata"`
		// 	Name string `xml:"name,attr"`
		// } `xml:"meta"` // Это было не совсем корректно
	} `xml:"metadata"`
}

// ExtractEPUBMetadata извлекает метаданные из EPUB файла
// Обновлена сигнатура для возврата seriesNumber
func ExtractEPUBMetadata(filePath string) (author, title, annotation, isbn, year, publisher, series, seriesNumber string, err error) {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return "", "", "", "", "", "", "", "", err
	}
	defer reader.Close()

	// Ищем container.xml
	var containerPath string
	for _, file := range reader.File {
		// Убедимся, что путь точно соответствует спецификации
		if strings.HasSuffix(strings.ToLower(file.Name), "meta-inf/container.xml") {
			containerPath = file.Name
			break
		}
	}

	if containerPath == "" {
		return "", "", "", "", "", "", "", "", fmt.Errorf("container.xml не найден")
	}

	// Читаем container.xml
	var containerFile *zip.File
	for _, file := range reader.File {
		if file.Name == containerPath {
			containerFile = file
			break
		}
	}

	if containerFile == nil {
		return "", "", "", "", "", "", "", "", fmt.Errorf("container.xml не найден по пути: %s", containerPath)
	}

	rc, err := containerFile.Open()
	if err != nil {
		return "", "", "", "", "", "", "", "", err
	}
	defer rc.Close()

	containerData, err := io.ReadAll(rc)
	if err != nil {
		return "", "", "", "", "", "", "", "", err
	}

	var container EPUBContainer
	err = xml.Unmarshal(containerData, &container)
	if err != nil {
		return "", "", "", "", "", "", "", "", fmt.Errorf("container.xml parse error: %v", err)
	}

	if len(container.Rootfiles) == 0 {
		return "", "", "", "", "", "", "", "", fmt.Errorf("rootfile не найден в container.xml")
	}

	// Ищем OPF файл
	opfPath := container.Rootfiles[0].Path
	var opfFile *zip.File
	for _, file := range reader.File {
		// Сравниваем полный путь или нормализованный путь
		if file.Name == opfPath {
			opfFile = file
			break
		}
	}

	if opfFile == nil {
		// Пробуем найти файл по частичному совпадению, если указанный путь не работает
		for _, file := range reader.File {
			if strings.HasSuffix(strings.ToLower(file.Name), ".opf") {
				opfFile = file
				// Можно добавить логирование, если найдено несколько .opf файлов
				break // Берем первый найденный .opf
			}
		}
		if opfFile == nil {
			return "", "", "", "", "", "", "", "", fmt.Errorf("OPF файл не найден по пути: %s", opfPath)
		}
	}

	// Читаем OPF файл
	rc, err = opfFile.Open()
	if err != nil {
		return "", "", "", "", "", "", "", "", err
	}
	defer rc.Close()

	opfData, err := io.ReadAll(rc)
	if err != nil {
		return "", "", "", "", "", "", "", "", err
	}

	var pkg EPUBPackage
	err = xml.Unmarshal(opfData, &pkg)
	if err != nil {
		return "", "", "", "", "", "", "", "", fmt.Errorf("OPF parse error: %v", err)
	}

	// Извлекаем название
	if len(pkg.Metadata.Title) > 0 {
		// Декодируем HTML-сущности
		title = html.UnescapeString(strings.TrimSpace(pkg.Metadata.Title[0]))
	}

	// Извлекаем авторов с корректным разделением имени и фамилии
	var authorNames []string
	for _, creator := range pkg.Metadata.Creators {
		// Декодируем HTML-сущности
		creatorName := html.UnescapeString(strings.TrimSpace(creator.Text))
		if creatorName != "" {
			firstName := ""
			lastName := ""

			// Согласно вашему запросу: последнее слово из dc:creator как lastName, первое как firstName
			parts := strings.Fields(creatorName)
			if len(parts) > 1 {
				lastName = parts[len(parts)-1] // Последнее слово
				firstName = parts[0]           // Первое слово
				// Если нужно использовать всё "имя" (все слова кроме последнего) как FirstName:
				// firstName = strings.Join(parts[:len(parts)-1], " ")
			} else if len(parts) == 1 {
				firstName = parts[0] // Или lastName = parts[0], в зависимости от предпочтений
				// Для одного слова будем считать его именем, как в вашем примере для DJVU/PDF
			}

			// Формируем имя автора без MiddleName
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
	}

	if len(authorNames) > 0 {
		author = strings.Join(authorNames, ", ") // Объединяем всех авторов через запятую
	}

	// Извлекаем аннотацию
	if len(pkg.Metadata.Description) > 0 {
		// Декодируем HTML-сущности
		annotation = html.UnescapeString(strings.TrimSpace(pkg.Metadata.Description[0].Text))
	}

	// Извлекаем ISBN и другие идентификаторы
	for _, identifier := range pkg.Metadata.Identifiers {
		// Декодируем HTML-сущности
		idText := html.UnescapeString(strings.TrimSpace(identifier.Text))
		// Проверяем атрибут scheme
		if strings.ToUpper(identifier.Scheme) == "ISBN" {
			isbn = idText
			break
		}
		// Проверяем текст на префикс ISBN
		if strings.HasPrefix(strings.ToUpper(idText), "ISBN:") ||
			strings.HasPrefix(strings.ToUpper(idText), "ISBN-10:") ||
			strings.HasPrefix(strings.ToUpper(idText), "ISBN-13:") {
			parts := strings.SplitN(idText, ":", 2)
			if len(parts) == 2 {
				isbn = strings.TrimSpace(parts[1])
				break
			}
		}
		// Проверяем текст на наличие подстроки ISBN
		if strings.Contains(strings.ToUpper(idText), "ISBN") {
			// Простая эвристика: убираем "ISBN" и пробелы
			cleaned := strings.Replace(strings.ToUpper(idText), "ISBN", "", -1)
			cleaned = strings.Replace(cleaned, "-", "", -1)
			cleaned = strings.Replace(cleaned, " ", "", -1)
			// Проверяем, похоже ли на ISBN10 (10 цифр) или ISBN13 (13 цифр)
			if len(cleaned) == 10 || len(cleaned) == 13 {
				// Простая проверка на цифры
				if _, err := strconv.Atoi(cleaned); err == nil {
					isbn = cleaned
					break
				}
			}
		}
		// Если ISBN не найден по схеме или префиксу, пробуем по содержимому
		// Проверяем, не является ли сам идентификатор похожим на ISBN
		cleanedId := strings.Replace(strings.Replace(strings.ToUpper(idText), "-", "", -1), " ", "", -1)
		if len(cleanedId) == 10 || len(cleanedId) == 13 {
			if _, err := strconv.Atoi(cleanedId); err == nil {
				isbn = idText // Сохраняем оригинальный формат
				break
			}
		}
	}

	// Извлекаем год
	if len(pkg.Metadata.Date) > 0 {
		// Декодируем HTML-сущности
		dateStr := html.UnescapeString(strings.TrimSpace(pkg.Metadata.Date[0].Text))
		// Извлекаем только год из даты (например, 2023-01-01 -> 2023)
		// Простой способ: берем первые 4 символа, если они цифры
		if len(dateStr) >= 4 {
			yearCandidate := dateStr[:4]
			if _, err := strconv.Atoi(yearCandidate); err == nil {
				year = yearCandidate
			}
		}
	}

	// Извлекаем издателя
	if len(pkg.Metadata.Publisher) > 0 {
		// Декодируем HTML-сущности
		publisher = html.UnescapeString(strings.TrimSpace(pkg.Metadata.Publisher[0].Text))
	}

	// Извлекаем серию и номер серии из мета-тегов
	// EPUB использует кастомные мета-теги для этой информации
	// Часто используемые стандарты:
	// 1. Calibre: <meta name="calibre:series" content="Название серии"/>
	//             <meta name="calibre:series_index" content="2"/>
	// 2. Другие редакторы могут использовать <meta property="belongs-to-collection">Серия</meta>
	//                                          <meta property="group-position">2</meta>
	// 3. Или просто кастомные теги <meta name="series" content="..."/> и <meta name="series_index" content="..."/>
	// 4. FB2 конвертация: <meta name="FB2.book-info.sequence" content="Название серии; number=9"/>

	// Ищем серию и номер
	for _, meta := range pkg.Metadata.Meta {
		// Проверяем атрибуты name/content (Calibre, FBReader и др.)
		if meta.Name != "" && meta.Content != "" {
			// Декодируем HTML-сущности в content
			content := html.UnescapeString(strings.TrimSpace(meta.Content))
			lowerName := strings.ToLower(meta.Name)

			// Специальная обработка для FB2.book-info.sequence
			if lowerName == "fb2.book-info.sequence" {
				// content уже декодирован
				// Формат: "Название серии; number=9"
				parts := strings.Split(content, "; number=")
				if len(parts) >= 1 {
					series = strings.TrimSpace(parts[0])
				}
				if len(parts) >= 2 {
					seriesNumber = strings.TrimSpace(parts[1])
				}
			} else if strings.Contains(lowerName, "series") && !strings.Contains(lowerName, "index") && !strings.Contains(lowerName, "number") {
				// Это, вероятно, название серии
				series = content
			} else if strings.Contains(lowerName, "series") && (strings.Contains(lowerName, "index") || strings.Contains(lowerName, "number")) {
				// Это, вероятно, номер серии
				seriesNumber = content
			}
			// Также можно проверить просто "series" и "series_index"
			if lowerName == "series" {
				series = content
			} else if lowerName == "series_index" || lowerName == "series-number" {
				seriesNumber = content
			}
		}
		// Проверяем атрибуты property (более современный EPUB 3 способ)
		if meta.Property != "" && meta.Text != "" {
			// Декодируем HTML-сущности в text
			text := html.UnescapeString(strings.TrimSpace(meta.Text))
			lowerProperty := strings.ToLower(meta.Property)
			if strings.Contains(lowerProperty, "belongs-to-collection") {
				// Это, вероятно, название серии
				series = text
			} else if strings.Contains(lowerProperty, "group-position") || strings.Contains(lowerProperty, "collection-sequence") {
				// Это, вероятно, номер серии
				seriesNumber = text
			}
		}
	}

	// Нормализация номера серии: убедимся, что это число, если возможно
	if seriesNumber != "" {
		// Простая нормализация: убираем пробелы
		seriesNumber = strings.TrimSpace(seriesNumber)
		// Можно попробовать преобразовать в число и обратно, чтобы убедиться, что это число
		// Но для простоты оставим как есть, так как в БД оно хранится как TEXT
	}

	if title == "" {
		return "", "", "", "", "", "", "", "", fmt.Errorf("пустое название")
	}

	return author, title, annotation, isbn, year, publisher, series, seriesNumber, nil
}
