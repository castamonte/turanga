// web/tags.go
package web

import (
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"turanga/config"
	"turanga/models"
)

// ShowTagHandler обрабатывает запросы к странице тега
// URL: /tag/{encoded_tag_name}
func (w *WebInterface) ShowTagHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Извлекаем имя тега из URL
	path := strings.TrimPrefix(r.URL.Path, "/tag/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Tag name is required", http.StatusBadRequest)
		return
	}

	// Декодируем URL-encoded имя тега
	tagName, err := url.QueryUnescape(path)
	if err != nil {
		http.Error(wr, "Invalid tag name", http.StatusBadRequest)
		return
	}

	// Получаем параметры пагинации из URL
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		p, err := strconv.Atoi(pageStr)
		if err == nil && p > 0 {
			page = p
		}
	}

	// Количество книг на странице - используем значение из конфигурации
	perPage := 60 // Значение по умолчанию
	if w.config != nil {
		// Используем PaginationThreshold из конфига для веб-интерфейса тоже
		perPage = w.config.PaginationThreshold
		if cfg.Debug {
			log.Printf("Используем порог пагинации из конфигурации для веб-интерфейса: %d", perPage)
		}
	} else {
		if cfg.Debug {
			log.Printf("Конфигурация не доступна для веб-интерфейса, используем порог по умолчанию: %d", perPage)
		}
	}
	offset := (page - 1) * perPage

	// Получаем общее количество книг с этим тегом
	var totalBooks int
	err = w.db.QueryRow(`
		SELECT COUNT(*) 
		FROM books b 
		JOIN book_tags bt ON b.id = bt.book_id
		JOIN tags t ON bt.tag_id = t.id
		WHERE t.name = ?
	`, tagName).Scan(&totalBooks)
	if err != nil {
		log.Printf("Database error getting total books count for tag %s: %v", tagName, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Получаем книги с этим тегом
	rows, err := w.db.Query(`
		SELECT b.id, b.title, b.file_hash,
		       CASE 
		           WHEN COUNT(a.id) > 2 THEN 'коллектив авторов'
		           WHEN COUNT(a.id) = 0 THEN 'Автор не указан'
		           ELSE GROUP_CONCAT(a.full_name, ', ')
		       END as authors_str
		FROM books b 
		JOIN book_tags bt ON b.id = bt.book_id
		JOIN tags t ON bt.tag_id = t.id
		LEFT JOIN book_authors ba ON b.id = ba.book_id
		LEFT JOIN authors a ON ba.author_id = a.id
		WHERE t.name = ?
		GROUP BY b.id, b.title, b.file_hash
		ORDER BY LOWER(b.title)
		LIMIT ? OFFSET ?
	`, tagName, perPage, offset)
	if err != nil {
		log.Printf("Database error getting tag books for name %s: %v", tagName, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Собираем книги
	var books []models.BookWeb
	for rows.Next() {
		var b models.BookWeb
		var fileHash sql.NullString
		var authorsStr string
		err := rows.Scan(&b.ID, &b.Title, &fileHash, &authorsStr)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error scanning tag book row: %v", err)
			}
			continue
		}

		// Получаем обложку по хешу файла
		if fileHash.Valid {
			b.FileHash = fileHash.String
			b.CoverURL = w.getCoverURLFromFileHash(fileHash.String, w.config)
		}

		b.AuthorsStr = authorsStr
		books = append(books, b)
	}

	if err = rows.Err(); err != nil {
		log.Printf("Error iterating tag book rows: %v", err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Вычисляем пагинацию
	totalPages := (totalBooks + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}

	// Определяем диапазон страниц для отображения
	pageRange := 5
	startPage := page - pageRange/2
	if startPage < 1 {
		startPage = 1
	}
	endPage := startPage + pageRange - 1
	if endPage > totalPages {
		endPage = totalPages
		startPage = endPage - pageRange + 1
		if startPage < 1 {
			startPage = 1
		}
	}

	// Подготавливаем данные для шаблона
	data := struct {
		TagName         string
		Books           []models.BookWeb
		CurrentPage     int
		TotalPages      int
		StartPage       int
		EndPage         int
		PageNumbers     []int
		PrevPage        int
		NextPage        int
		IsAuthenticated bool
	}{
		TagName:         tagName,
		Books:           books,
		CurrentPage:     page,
		TotalPages:      totalPages,
		StartPage:       startPage,
		EndPage:         endPage,
		PrevPage:        page - 1,
		NextPage:        page + 1,
		IsAuthenticated: w.isAuthenticated(r),
	}

	// Генерируем список номеров страниц
	for i := startPage; i <= endPage; i++ {
		data.PageNumbers = append(data.PageNumbers, i)
	}

	// Загружаем шаблон из файла
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "tag.html")
	tmpl, err := template.New("tag").Funcs(template.FuncMap{
		"sub":        func(a, b int) int { return a - b },
		"urlquery":   url.QueryEscape,
		"formatSize": FormatFileSize, // Добавим на всякий случай
	}).ParseFiles(tmplPath)
	if err != nil {
		log.Printf("Error parsing tag template: %v", err)
		http.Error(wr, "Template error", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон
	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "tag", data); err != nil {
		log.Printf("Error executing tag template for name %s: %v", tagName, err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
