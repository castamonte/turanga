package opds

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"turanga/config"
	"turanga/models"
	"turanga/web"
)

// Вспомогательные структуры для XML (только для поиска)
type Link struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
}

type Content struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

type Author struct {
	Name string `xml:"name"`
}

type Entry struct {
	XMLName  xml.Name `xml:"entry"`
	Title    string   `xml:"title"`
	ID       string   `xml:"id"`
	Updated  string   `xml:"updated"`
	Author   Author   `xml:"author"`
	Language string   `xml:"dc:language"`
	Issued   string   `xml:"dc:issued"`
	Content  Content  `xml:"content"`
	Links    []Link   `xml:"link"`
}

type SearchFeed struct {
	XMLName   xml.Name `xml:"feed"`
	Xmlns     string   `xml:"xmlns,attr"`
	XmlnsDc   string   `xml:"xmlns:dc,attr,omitempty"`
	XmlnsOpds string   `xml:"xmlns:opds,attr,omitempty"`
	Title     string   `xml:"title"`
	ID        string   `xml:"id"`
	Updated   string   `xml:"updated"`
	Links     []Link   `xml:"link"`
	Entries   []Entry  `xml:"entry"`
}

// IndexHandler обрабатывает корневой маршрут "/"
func IndexHandler(webInterface *web.WebInterface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Для HTML запросов показываем веб-интерфейс
		if strings.Contains(r.Header.Get("Accept"), "text/html") && r.URL.Path == "/" {
			webInterface.ShowWebInterface(w, r)
			return
		}
		// Для OPDS запросов показываем каталог
		ShowOPDSCatalog(w, r)
	}
}

// ShowOPDSCatalogHandler обрабатывает маршрут "/feed"
func ShowOPDSCatalogHandler(w http.ResponseWriter, r *http.Request) {
	ShowOPDSCatalog(w, r)
}

// ShowOPDSCatalog отображает корневой OPDS каталог
func ShowOPDSCatalog(w http.ResponseWriter, r *http.Request) {

	// Создаем OPDS фид
	feed := models.NewFeed("Каталог книг")
	feed.Updated = time.Now().Format("2006-01-02T15:04:05+00:00")
	feed.Icon = "/static/opds-icons/leela.png"

	// Ссылка на поисковый механизм OPDS
	feed.Links = append(feed.Links, models.Link{
		Rel:  "search",
		Type: "application/opds.search+xml",
		Href: "/opds-search/{searchTerms}",
	})

	// Добавляем категории поиска
	categories := []struct {
		title         string
		id            string
		content       string
		href          string
		rel           string
		thumbnailPath string
	}{
		{"Авторы", "turanga:authors", "Книги по фамилии автора", "/authors", "subsection", "/static/opds-icons/authors.png"},
		{"Серии", "turanga:series", "Книги по названию серии", "/series", "subsection", "/static/opds-icons/series.png"},
		{"Все книги", "turanga:books", "Книги по названию", "/books", "subsection", "/static/opds-icons/books.png"},
		{"Теги", "turanga:tags", "Книги по тегам", "/tags", "subsection", "/static/opds-icons/tags.png"},
		{"Новые поступления", "turanga:recent", "Последние добавленные книги", "/recent", "http://opds-spec.org/sort/new", "/static/opds-icons/recent.png"},
	}

	for _, cat := range categories {
		entry := models.Entry{
			Title:   cat.title,
			Updated: time.Now().Format("2006-01-02T15:04:05+00:00"),
			ID:      cat.id,
			Content: models.Content{Type: "text", Text: cat.content},
			Links: []models.Link{{
				Href: cat.href,
				Type: "application/atom+xml;profile=opds-catalog;kind=navigation",
				Rel:  cat.rel,
			}},
		}
		// Добавляем ссылку на миниатюру, если путь задан
		if cat.thumbnailPath != "" {
			entry.Links = append(entry.Links, models.Link{
				Href: cat.thumbnailPath,
				Type: GetMimeTypeFromPath(cat.thumbnailPath),
				Rel:  "http://opds-spec.org/thumbnail",
			})
		}

		feed.Entries = append(feed.Entries, entry)
	}

	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog; charset=utf-8")
	if err := xml.NewEncoder(w).Encode(feed); err != nil {
		log.Printf("Ошибка кодирования XML: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// OPDSSearchHandler обрабатывает поисковые запросы OPDS
// URL: /opds-search/{searchTerms}
func OPDSSearchHandler(webInterface *web.WebInterface) http.HandlerFunc {
	cfg := config.GetConfig()
	return func(w http.ResponseWriter, r *http.Request) {
		// Извлекаем поисковый запрос из URL
		query := strings.TrimPrefix(r.URL.Path, "/opds-search/")
		query, err := url.QueryUnescape(query)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка декодирования поискового запроса: %v", err)
			}
			query = strings.TrimPrefix(r.URL.Path, "/opds-search/")
		}

		if query == "" {
			http.Error(w, "Search term is required", http.StatusBadRequest)
			return
		}

		// Выполнить поиск в БД
		db := webInterface.GetDB()
		if db == nil {
			log.Printf("Ошибка: БД не инициализирована для OPDS поиска")
			http.Error(w, "Database not initialized", http.StatusInternalServerError)
			return
		}

		searchPattern := "%" + query + "%"

		rows, err := db.Query(`
        SELECT b.id, b.title, b.file_type, b.file_hash, b.published_at,
               (SELECT CASE
                   WHEN COUNT(*) > 5 THEN 'Коллектив авторов'
                   WHEN COUNT(*) = 0 THEN 'Автор не указан'
                   ELSE GROUP_CONCAT(a.full_name, ', ')
                END
                FROM book_authors ba
                LEFT JOIN authors a ON ba.author_id = a.id
                WHERE ba.book_id = b.id) as authors_str
        FROM books b
        WHERE b.title LIKE ? COLLATE ICU_NOCASE 
           OR EXISTS (
                SELECT 1 FROM book_authors ba
                JOIN authors a ON ba.author_id = a.id
                WHERE ba.book_id = b.id AND a.full_name LIKE ? COLLATE ICU_NOCASE
           )
           OR IFNULL(b.series, '') LIKE ? COLLATE ICU_NOCASE
        GROUP BY b.id, b.title, b.file_type, b.file_hash, b.published_at
        ORDER BY b.title
        LIMIT 50`,
			searchPattern, searchPattern, searchPattern)

		if err != nil {
			log.Printf("Ошибка БД в OPDS поиске: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		// Создаем структурированный фид
		feed := &SearchFeed{
			Xmlns:     "http://www.w3.org/2005/Atom",
			XmlnsDc:   "http://purl.org/dc/terms/",
			XmlnsOpds: "http://opds-spec.org/2010/catalog",
			Title:     fmt.Sprintf("Результаты поиска для '%s'", html.EscapeString(query)),
			ID:        fmt.Sprintf("urn:uuid:%s", time.Now().Format("20060102150405")),
			Updated:   time.Now().Format(time.RFC3339),
			Links: []Link{
				{
					Rel:  "self",
					Type: "application/atom+xml;profile=opds-catalog;kind=acquisition",
					Href: "/opds-search/" + url.QueryEscape(query),
				},
				{
					Rel:  "start",
					Type: "application/atom+xml;profile=opds-catalog;kind=navigation",
					Href: "/feed",
				},
			},
		}

		// Генерируем записи для найденных книг
		for rows.Next() {
			var id int
			var title, fileType, fileHash, publishedAt, authorsStr sql.NullString

			err := rows.Scan(&id, &title, &fileType, &fileHash, &publishedAt, &authorsStr)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка сканирования книги в OPDS поиске: %v", err)
				}
				continue
			}

			// Формируем запись книги в формате OPDS
			entry := generateOPDSEntry(webInterface, id, title.String, authorsStr.String, fileType.String, fileHash.String, publishedAt.String)
			feed.Entries = append(feed.Entries, entry)
		}

		if err = rows.Err(); err != nil {
			log.Printf("Ошибка итерации по результатам OPDS поиска: %v", err)
			http.Error(w, "Database iteration error", http.StatusInternalServerError)
			return
		}

		// Устанавливаем заголовки и отправляем ответ
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=acquisition; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300") // Кэширование на 5 минут

		if err := xml.NewEncoder(w).Encode(feed); err != nil {
			log.Printf("Ошибка кодирования XML в OPDS поиске: %v", err)
			http.Error(w, "XML encoding error", http.StatusInternalServerError)
			return
		}
	}
}

// OPDSDownloadBookHandler обрабатывает скачивание книги для OPDS
func OPDSDownloadBookHandler(db *sql.DB, rootPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := config.GetConfig()
		if cfg.Debug {
			log.Printf("OPDS Download request: %s %s", r.Method, r.URL.Path)
			log.Printf("User-Agent: %s", r.Header.Get("User-Agent"))
			log.Printf("Accept header: %s", r.Header.Get("Accept"))
		}

		// Извлекаем ID книги из URL
		path := strings.TrimPrefix(r.URL.Path, "/opds-download/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 1 {
			log.Printf("Invalid URL format: %s", r.URL.Path)
			http.Error(w, "Invalid URL", http.StatusBadRequest)
			return
		}

		id, err := strconv.Atoi(parts[0])
		if err != nil {
			log.Printf("Invalid book ID in URL: %s, error: %v", parts[0], err)
			http.Error(w, "Invalid book ID", http.StatusBadRequest)
			return
		}

		// Получаем информацию о книге из БД
		var fileURL, fileType, fileHash sql.NullString
		err = db.QueryRow(`
            SELECT file_url, file_type, file_hash 
            FROM books 
            WHERE id = ?`, id).Scan(&fileURL, &fileType, &fileHash)

		if err != nil {
			if err == sql.ErrNoRows {
				log.Printf("Book not found: %d", id)
				http.Error(w, "Book not found", http.StatusNotFound)
			} else {
				log.Printf("Database error getting book %d: %v", id, err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}

		if !fileURL.Valid || !fileType.Valid || !fileHash.Valid {
			log.Printf("Book data is incomplete for book %d", id)
			http.Error(w, "Book data is incomplete", http.StatusInternalServerError)
			return
		}

		// Теперь fileURL содержит абсолютный путь, используем его напрямую
		filePath := fileURL.String

		// Проверяем существование файла
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			log.Printf("File not found on disk: %s", filePath)
			http.Error(w, "File not found on disk", http.StatusNotFound)
			return
		} else if err != nil {
			log.Printf("Error checking file %s: %v", filePath, err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Устанавливаем правильные заголовки
		mimeType := GetMimeType(fileType.String)
		w.Header().Set("Content-Type", mimeType)

		// Используем оригинальное имя файла из пути, а не формируем новое
		originalFilename := filepath.Base(filePath)

		userAgent := r.Header.Get("User-Agent")
		if strings.Contains(strings.ToLower(userAgent), "fbreader") ||
			strings.Contains(strings.ToLower(userAgent), "reader") ||
			strings.Contains(strings.ToLower(userAgent), "opds") {
			// Для ридеров используем inline для прямого открытия
			w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", url.QueryEscape(originalFilename)))
		} else {
			// Для браузеров используем attachment
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", url.QueryEscape(originalFilename)))
		}

		// Добавляем заголовки для кэширования
		w.Header().Set("Cache-Control", "public, max-age=3600")

		// Отправляем файл
		http.ServeFile(w, r, filePath)
	}
}

// generateOPDSEntry генерирует структурированную запись книги для OPDS
func generateOPDSEntry(webInterface *web.WebInterface, id int, title, authors, fileType, fileHash, publishedAt string) Entry {
	// Получаем URL обложки
	coverURL := ""
	if fileHash != "" {
		coverURL = webInterface.GetCoverURLFromFileHash(fileHash)
	}

	// Получаем аннотацию
	annotation := ""
	if fileHash != "" {
		annotation = webInterface.GetAnnotationFromFile(id, fileHash)
	}

	// Формируем URL для скачивания через OPDS обработчик
	// ВАЖНО: используем OPDSDownloadBookHandler, а не прямой путь к файлу
	downloadURL := fmt.Sprintf("/opds-download/%d/%s", id, url.QueryEscape(title+"."+GetFileExtension(fileType)))

	// Формируем содержимое
	contentText := fmt.Sprintf("Автор(ы): %s\nГод: %s", authors, publishedAt)
	if annotation != "" {
		if len(annotation) > 2000 {
			annotation = annotation[:2000] + "..."
		}
		contentText += fmt.Sprintf("\n\nОписание:\n%s", annotation)
	}

	// Создаем базовую запись
	entry := Entry{
		Title:    html.EscapeString(title),
		ID:       fmt.Sprintf("urn:book:%d", id),
		Updated:  time.Now().Format(time.RFC3339),
		Author:   Author{Name: html.EscapeString(authors)},
		Language: "ru",
		Issued:   publishedAt,
		Content: Content{
			Type: "text",
			Text: html.EscapeString(contentText),
		},
		Links: []Link{
			{
				Rel:  "http://opds-spec.org/acquisition",
				Type: GetMimeType(fileType),
				Href: downloadURL,
			},
		},
	}

	// Добавляем ссылку на обложку, если есть
	if coverURL != "" {
		imageType := "image/jpeg"
		if strings.HasSuffix(coverURL, ".png") {
			imageType = "image/png"
		} else if strings.HasSuffix(coverURL, ".gif") {
			imageType = "image/gif"
		} else if strings.HasSuffix(coverURL, ".webp") {
			imageType = "image/webp"
		}

		entry.Links = append(entry.Links, Link{
			Rel:  "http://opds-spec.org/image",
			Type: imageType,
			Href: coverURL,
		})

		// Добавляем thumbnail ссылку
		entry.Links = append(entry.Links, Link{
			Rel:  "http://opds-spec.org/image/thumbnail",
			Type: imageType,
			Href: coverURL,
		})
	}

	return entry
}
