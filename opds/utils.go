// opds/utils.go
package opds

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"turanga/config"
	"turanga/models"
)

// Global variable to store root path
var rootPath string

// SetRootPath устанавливает корневую директорию приложения
func SetRootPath(path string) {
	rootPath = path
}

// readAnnotation читает аннотацию из файла notes/{file_hash}.txt
func readAnnotation(fileHash string) string {
	cfg := config.GetConfig()
	if fileHash == "" {
		return ""
	}

	// Используем корневую директорию приложения
	notesDir := filepath.Join(rootPath, "notes")
	annotationPath := filepath.Join(notesDir, fileHash+".txt")

	// Проверяем существование файла
	if _, err := os.Stat(annotationPath); os.IsNotExist(err) {
		if cfg.Debug {
			log.Printf("Файл аннотации не найден: %s", annotationPath)
		}
		return ""
	}

	// Читаем содержимое файла
	content, err := ioutil.ReadFile(annotationPath)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка чтения файла аннотации %s: %v", annotationPath, err)
		}
		return ""
	}

	return string(content)
}

// GetMimeType возвращает MIME-тип для расширения файла
func GetMimeType(fileType string) string {
	mimeTypes := map[string]string{
		"fb2":     "application/fb2+xml",
		"fb2.zip": "application/fb2+zip",
		"epub":    "application/epub+zip",
		"pdf":     "application/pdf",
		"djvu":    "image/vnd.djvu",
		"zip":     "application/zip",
	}

	fileType = strings.ToLower(fileType)

	if mime, ok := mimeTypes[fileType]; ok {
		return mime
	}

	// fallback
	switch fileType {
	case "fb2":
		return "application/fb2+xml"
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	case "djvu", "djv":
		return "image/vnd.djvu"
	case "zip":
		return "application/zip"
	case "txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// GetFileExtension возвращает расширение файла по типу файла
func GetFileExtension(fileType string) string {
	fileType = strings.ToLower(fileType)

	if fileType == "fb2.zip" {
		return "fb2.zip" // Важно сохранить точку
	}

	switch fileType {
	case "fb2":
		return "fb2"
	case "epub":
		return "epub"
	case "pdf":
		return "pdf"
	case "djvu":
		return "djvu"
	case "djv":
		return "djv"
	default:
		if fileType != "" {
			return fileType
		}
		return "file"
	}
}

// FormatBookContentForOPDS форматирует содержимое книги для OPDS каталога
func FormatBookContentForOPDS(book *models.Book) string {
	var parts []string

	// Добавляем серию и номер (только это оставляем)
	if book.Series != "" {
		seriesInfo := "Серия: " + book.Series
		if book.SeriesNumber != "" {
			seriesInfo += " #" + book.SeriesNumber
		}
		parts = append(parts, seriesInfo)
	}

	// Основная информация
	mainInfo := strings.Join(parts, " | ")

	// Добавляем аннотацию
	if book.Annotation != "" {
		if mainInfo != "" {
			mainInfo += "\n\n"
		}

		annotation := strings.TrimSpace(book.Annotation)
		if len(annotation) > 2025 {
			annotation = annotation[:2000] + "..."
		}

		mainInfo += annotation
	}

	if mainInfo == "" {
		return "Информация отсутствует"
	}

	return mainInfo
}

// GetMimeTypeFromPath возвращает MIME-тип по пути к файлу
func GetMimeTypeFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

// OPDSResponse общая структура для OPDS ответов
type OPDSResponse struct {
	Title         string
	Description   string
	Entries       []models.Entry
	IsAcquisition bool
}

// RenderOPDSFeed рендерит OPDS фид
func RenderOPDSFeed(w http.ResponseWriter, title, description string, entries []models.Entry, isAcquisition bool) {
	feed := models.NewFeed(title)
	feed.Updated = time.Now().Format("2006-01-02T15:04:05+00:00")
	feed.Entries = entries

	contentType := "application/atom+xml;profile=opds-catalog"
	if isAcquisition {
		contentType += ";kind=acquisition"
	} else {
		contentType += ";kind=navigation"
	}
	contentType += "; charset=utf-8"

	w.Header().Set("Content-Type", contentType)
	if err := xml.NewEncoder(w).Encode(feed); err != nil {
		log.Printf("Ошибка кодирования XML для фида '%s': %v", title, err)
		http.Error(w, "XML encoding error: "+err.Error(), http.StatusInternalServerError)
	}
}

// CreateNavigationEntry создает навигационную запись
func CreateNavigationEntry(title, id, content, href, rel string, thumbnailPath string) models.Entry {
	entry := models.Entry{
		Title:   title,
		Updated: time.Now().Format("2006-01-02T15:04:05+00:00"),
		ID:      id,
		Content: models.Content{Type: "text", Text: content},
		Links: []models.Link{{
			Href: href,
			Type: "application/atom+xml;profile=opds-catalog;kind=navigation",
			Rel:  rel,
		}},
	}

	// Добавляем ссылку на миниатюру, если путь задан
	if thumbnailPath != "" {
		entry.Links = append(entry.Links, models.Link{
			Href: thumbnailPath,
			Type: GetMimeTypeFromPath(thumbnailPath),
			Rel:  "http://opds-spec.org/thumbnail",
		})
	}

	return entry
}

// CreateAcquisitionEntry создает запись с книгой для скачивания
func CreateAcquisitionEntry(book *models.Book) models.Entry {
	if len(book.Files) == 0 {
		return models.Entry{} // Пустая запись если нет файлов
	}

	content := FormatBookContentForOPDS(book)
	updatedTime := time.Now().Format("2006-01-02T15:04:05+00:00")

	entry := models.Entry{
		Title:   book.Title,
		ID:      "turanga:book:" + fmt.Sprintf("%d", book.ID),
		Updated: updatedTime,
		Content: models.Content{Type: "text", Text: content},
		Links:   []models.Link{},
	}

	// Добавляем каждого автора в entry.Authors
	for _, author := range book.Authors {
		if author.FullName != "" {
			entry.Authors = append(entry.Authors, models.AuthorInfoForOPDS{
				Name: author.FullName,
			})
		}
	}

	// Добавляем ссылку на обложку
	if len(book.Files) > 0 && book.Files[0].FileHash != "" {
		coverLink := models.Link{
			Href: "/covers/" + book.Files[0].FileHash + ".jpg",
			Type: "image/jpeg",
			Rel:  "http://opds-spec.org/image",
		}
		entry.Links = append(entry.Links, coverLink)

		thumbnailLink := models.Link{
			Href: "/covers/" + book.Files[0].FileHash + ".jpg",
			Type: "image/jpeg",
			Rel:  "http://opds-spec.org/image/thumbnail",
		}
		entry.Links = append(entry.Links, thumbnailLink)
	}

	// Добавляем ссылки на файлы для скачивания через OPDS обработчик
	for _, file := range book.Files {
		// Формируем правильный URL для скачивания
		downloadURL := fmt.Sprintf("/opds-download/%d/%s", book.ID, url.QueryEscape(book.Title+"."+GetFileExtension(file.Type)))

		entry.Links = append(entry.Links, models.Link{
			Href: downloadURL,
			Type: GetMimeType(file.Type),
			Rel:  "http://opds-spec.org/acquisition",
		})
	}

	return entry
}

// CreatePlaceholders создает строку с плейсхолдерами для SQL запроса
func CreatePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	places := make([]string, n)
	for i := 0; i < n; i++ {
		places[i] = "?"
	}
	return strings.Join(places, ",")
}

// ConvertToInterfaceSlice преобразует []int в []interface{}
func ConvertToInterfaceSlice(slice []int) []interface{} {
	interfaces := make([]interface{}, len(slice))
	for i, v := range slice {
		interfaces[i] = v
	}
	return interfaces
}

// SortBooksByTitle сортирует книги по названию
func SortBooksByTitle(booksMap map[int]*models.Book) []*models.Book {
	var books []*models.Book
	for _, book := range booksMap {
		books = append(books, book)
	}

	sort.Slice(books, func(i, j int) bool {
		return books[i].Title < books[j].Title
	})

	return books
}

// SortBooksByID сортирует книги по ID (новые первыми)
func SortBooksByID(booksMap map[int]*models.Book) []*models.Book {
	var books []*models.Book
	for _, book := range booksMap {
		books = append(books, book)
	}

	sort.Slice(books, func(i, j int) bool {
		return books[i].ID > books[j].ID
	})

	return books
}

// GetBooksByIDs получает книги по списку ID с авторами одним оптимизированным запросом
func GetBooksByIDs(db *sql.DB, bookIDs []int) (map[int]*models.Book, error) {
	cfg := config.GetConfig()
	if len(bookIDs) == 0 {
		return make(map[int]*models.Book), nil
	}

	placeholders := CreatePlaceholders(len(bookIDs))
	query := fmt.Sprintf(`
        SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
               b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
        FROM books b
        WHERE b.id IN (%s)
          AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
          AND (b.over18 IS NULL OR b.over18 = 0)
        ORDER BY b.title
    `, placeholders)

	args := ConvertToInterfaceSlice(bookIDs)
	bookRows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer bookRows.Close()

	booksMap := make(map[int]*models.Book)

	// Сначала создаем книги
	for bookRows.Next() {
		var bookID int
		var title, series, seriesNumber, publishedAt, isbn, year, publisher sql.NullString
		var fileURL, fileType, fileHash sql.NullString

		err := bookRows.Scan(&bookID, &title, &series, &seriesNumber, &publishedAt,
			&isbn, &year, &publisher, &fileURL, &fileType, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги: %v", err)
			}
			continue
		}

		// Читаем аннотацию из файла
		annotationText := readAnnotation(fileHash.String)

		book := &models.Book{
			ID:           bookID,
			Title:        title.String,
			Authors:      []models.Author{},
			Series:       series.String,
			SeriesNumber: seriesNumber.String,
			PublishedAt:  publishedAt.String,
			Annotation:   annotationText,
			ISBN:         isbn.String,
			Year:         year.String,
			Publisher:    publisher.String,
			Files:        []models.BookFile{},
			Tags:         []string{},
		}

		bookFile := models.BookFile{
			URL:      fmt.Sprintf("/opds-download/%d/%s", bookID, url.QueryEscape(title.String+"."+GetFileExtension(fileType.String))),
			Type:     fileType.String,
			FileHash: fileHash.String,
		}
		book.Files = append(book.Files, bookFile)

		booksMap[bookID] = book
	}

	// Затем получаем всех авторов одним запросом
	if len(bookIDs) > 0 {
		placeholders := CreatePlaceholders(len(bookIDs))
		authorQuery := fmt.Sprintf(`
            SELECT ba.book_id, a.full_name
            FROM book_authors ba
            JOIN authors a ON ba.author_id = a.id
            WHERE ba.book_id IN (%s)
            ORDER BY ba.book_id, a.full_name
        `, placeholders)

		authorArgs := ConvertToInterfaceSlice(bookIDs)
		authorRows, err := db.Query(authorQuery, authorArgs...)
		if err == nil {
			defer authorRows.Close()

			// Заполняем авторов для каждой книги
			for authorRows.Next() {
				var bookID int
				var fullName string
				if err := authorRows.Scan(&bookID, &fullName); err != nil {
					continue
				}

				if book, exists := booksMap[bookID]; exists {
					author := models.Author{
						FullName: fullName,
					}
					book.Authors = append(book.Authors, author)
				}
			}
		}
	}

	return booksMap, nil
}

// GetBooksWithAuthors получает книги с авторами по произвольному запросу
func GetBooksWithAuthors(db *sql.DB, query string, args ...interface{}) (map[int]*models.Book, error) {
	cfg := config.GetConfig()
	// Получаем книги
	bookRows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer bookRows.Close()

	booksMap := make(map[int]*models.Book)
	var bookIDs []int

	// Сначала создаем книги
	for bookRows.Next() {
		var bookID int
		var title, series, seriesNumber, publishedAt, isbn, year, publisher sql.NullString
		var fileURL, fileType, fileHash sql.NullString

		err := bookRows.Scan(&bookID, &title, &series, &seriesNumber, &publishedAt,
			&isbn, &year, &publisher, &fileURL, &fileType, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги: %v", err)
			}
			continue
		}

		// Читаем аннотацию из файла
		annotationText := readAnnotation(fileHash.String)

		book := &models.Book{
			ID:           bookID,
			Title:        title.String,
			Authors:      []models.Author{},
			Series:       series.String,
			SeriesNumber: seriesNumber.String,
			PublishedAt:  publishedAt.String,
			Annotation:   annotationText,
			ISBN:         isbn.String,
			Year:         year.String,
			Publisher:    publisher.String,
			Files:        []models.BookFile{},
			Tags:         []string{},
		}

		bookFile := models.BookFile{
			URL:      fmt.Sprintf("/opds-download/%d/%s", bookID, url.QueryEscape(title.String+"."+GetFileExtension(fileType.String))),
			Type:     fileType.String,
			FileHash: fileHash.String,
		}
		book.Files = append(book.Files, bookFile)

		booksMap[bookID] = book
		bookIDs = append(bookIDs, bookID)
	}

	// Затем получаем всех авторов одним запросом
	if len(bookIDs) > 0 {
		placeholders := CreatePlaceholders(len(bookIDs))
		authorQuery := fmt.Sprintf(`
            SELECT ba.book_id, a.full_name
            FROM book_authors ba
            JOIN authors a ON ba.author_id = a.id
            WHERE ba.book_id IN (%s)
            ORDER BY ba.book_id, a.full_name
        `, placeholders)

		authorArgs := ConvertToInterfaceSlice(bookIDs)
		authorRows, err := db.Query(authorQuery, authorArgs...)
		if err == nil {
			defer authorRows.Close()

			// Заполняем авторов для каждой книги
			for authorRows.Next() {
				var bookID int
				var fullName string
				if err := authorRows.Scan(&bookID, &fullName); err != nil {
					continue
				}

				if book, exists := booksMap[bookID]; exists {
					author := models.Author{
						FullName: fullName,
					}
					book.Authors = append(book.Authors, author)
				}
			}
		}
	}

	return booksMap, nil
}

// GetBooksByLetter получает книги на определенную букву
func GetBooksByLetter(db *sql.DB, letter string, field string) (map[int]*models.Book, error) {
	var query string
	var args []interface{}

	// Определяем условие для буквы
	switch strings.ToUpper(letter) {
	case "А", "Б", "В", "Г", "Д", "Е", "Ж", "З", "И", "Й", "К", "Л", "М",
		"Н", "О", "П", "Р", "С", "Т", "У", "Ф", "Х", "Ц", "Ч", "Ш", "Щ", "Ъ", "Ы", "Ь", "Э", "Ю", "Я":
		// Русские буквы
		if strings.ToUpper(letter) == "Ё" {
			query = `
                SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
                       b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
                FROM books b
                WHERE (SUBSTR(b.title, 1, 1) = 'Ё' OR SUBSTR(b.title, 1, 1) = 'Е')
                  AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
                  AND (b.over18 IS NULL OR b.over18 = 0)
                ORDER BY b.title
            `
		} else {
			query = `
                SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
                       b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
                FROM books b
                WHERE SUBSTR(b.title, 1, 1) = ?
                  AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
                  AND (b.over18 IS NULL OR b.over18 = 0)
                ORDER BY b.title
            `
			args = append(args, letter)
		}
	case "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
		"N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z":
		// Английские буквы
		query = `
            SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
                   b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
            FROM books b
            WHERE UPPER(SUBSTR(b.title, 1, 1)) = ?
              AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b.over18 IS NULL OR b.over18 = 0)
            ORDER BY b.title
        `
		args = append(args, strings.ToUpper(letter))
	default:
		// Другие символы
		query = `
            SELECT b.id as book_id, b.title, b.series, b.series_number, b.published_at,
                   b.isbn, b.year, b.publisher, b.file_url, b.file_type, b.file_hash
            FROM books b
            WHERE UPPER(SUBSTR(b.title, 1, 1)) = UPPER(?)
              AND b.file_type IN ('epub', 'fb2', 'fb2.zip')
              AND (b.over18 IS NULL OR b.over18 = 0)
            ORDER BY b.title
        `
		args = append(args, letter)
	}

	return GetBooksWithAuthors(db, query, args...)
}

// CreateAlphabetEntries создает записи для алфавитной навигации
func CreateAlphabetEntries(letterRows *sql.Rows, baseURL, titlePrefix string) ([]models.Entry, error) {
	var entries []models.Entry

	for letterRows.Next() {
		var firstLetter string
		var count int
		if err := letterRows.Scan(&firstLetter, &count); err != nil {
			continue
		}

		entry := CreateNavigationEntry(
			firstLetter,
			fmt.Sprintf("turanga:%s:letter:%s", strings.TrimPrefix(baseURL, "/"), firstLetter),
			fmt.Sprintf("%s на букву \"%s\" (%d шт.)", titlePrefix, firstLetter, count),
			fmt.Sprintf("%s/%s", baseURL, firstLetter),
			"subsection",
			"",
		)
		entries = append(entries, entry)
	}

	return entries, letterRows.Err()
}

// Пример действительно общей утилиты (если понадобится):
// EscapeForXML экранирует специальные символы для XML
func EscapeForXML(s string) string {
	// Используем стандартную библиотеку
	// Но в OPDS мы используем xml.Marshal, который сам экранирует.
	// Эта функция может быть не нужна.
	// return xml.EscapeText(nil, []byte(s))
	// Для демонстрации оставим простую замену
	replacements := map[string]string{
		"&":  "&amp;",
		"<":  "<",
		">":  ">",
		"\"": "&quot;",
		"'":  "&apos;",
	}
	result := s
	for old, new := range replacements {
		result = strings.ReplaceAll(result, old, new)
	}
	return result
}
