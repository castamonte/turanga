// web/series.go
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

// ShowSeriesHandler обрабатывает запросы к странице серии
// URL: /s/{encoded_series_name}
func (w *WebInterface) ShowSeriesHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Извлекаем имя серии из URL
	path := strings.TrimPrefix(r.URL.Path, "/s/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Series name is required", http.StatusBadRequest)
		return
	}

	// Декодируем URL-encoded имя серии
	seriesName, err := url.QueryUnescape(path)
	if err != nil {
		http.Error(wr, "Invalid series name", http.StatusBadRequest)
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

	// Получаем общее количество книг в серии
	var totalBooks int
	err = w.db.QueryRow(`
		SELECT COUNT(*) 
		FROM books b 
		WHERE b.series = ?
	`, seriesName).Scan(&totalBooks)
	if err != nil {
		log.Printf("Database error getting total books count for series %s: %v", seriesName, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Получаем книги серии, отсортированные по номеру в серии
	rows, err := w.db.Query(`
		SELECT b.id, b.title, b.series_number,
		       (SELECT GROUP_CONCAT(a.full_name, ', ') 
		        FROM book_authors ba 
		        LEFT JOIN authors a ON ba.author_id = a.id 
		        WHERE ba.book_id = b.id) as authors_str,
		       b.file_hash
		FROM books b 
		WHERE b.series = ?
		ORDER BY 
			CASE 
				WHEN b.series_number GLOB '[0-9]*' THEN CAST(b.series_number AS INTEGER)
				ELSE 0
			 END,
			LOWER(b.series_number),
			LOWER(b.title)
		LIMIT ? OFFSET ?
	`, seriesName, perPage, offset)
	if err != nil {
		log.Printf("Database error getting series books for name %s: %v", seriesName, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Собираем книги
	var books []models.BookWeb
	for rows.Next() {
		var b models.BookWeb
		var seriesNumber sql.NullString
		var authorsStr string
		var fileHash sql.NullString
		err := rows.Scan(&b.ID, &b.Title, &seriesNumber, &authorsStr, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error scanning series book row: %v", err)
			}
			continue
		}

		if seriesNumber.Valid {
			b.SeriesNumber = seriesNumber.String
		}
		b.AuthorsStr = authorsStr

		// Получаем URL обложки по хешу
		if fileHash.Valid {
			b.CoverURL = w.getCoverURLFromFileHash(fileHash.String, w.config)
		}

		books = append(books, b)
	}

	if err = rows.Err(); err != nil {
		log.Printf("Error iterating series book rows: %v", err)
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
		SeriesName      string
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
		SeriesName:      seriesName,
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

	// Загружаем шаблон (предполагаем, что у вас есть метод loadTemplates или подобный)
	// Предполагая, что у WebInterface есть поле rootPath
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "series.html")
	tmpl, err := template.New("series").Funcs(template.FuncMap{
		"sub":        func(a, b int) int { return a - b },
		"urlquery":   url.QueryEscape,
		"formatSize": FormatFileSize, // Добавим на всякий случай
	}).ParseFiles(tmplPath)

	if err != nil {
		log.Printf("Error parsing series template: %v", err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон
	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "series", data); err != nil { // Выполняем именованный шаблон "series"
		log.Printf("Error executing series template for name %s: %v", seriesName, err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// SaveSeriesHandler обрабатывает сохранение изменений серии
func (w *WebInterface) SaveSeriesHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Извлекаем имя серии из URL
	path := strings.TrimPrefix(r.URL.Path, "/save/series/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Series name is required", http.StatusBadRequest)
		return
	}

	// Декодируем URL-encoded имя серии
	oldSeriesName, err := url.QueryUnescape(path)
	if err != nil {
		http.Error(wr, "Invalid series name", http.StatusBadRequest)
		return
	}

	// Только POST запросы
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем новое имя из формы
	newName := strings.TrimSpace(r.FormValue("name"))
	if newName == "" {
		http.Error(wr, "Название серии не может быть пустым", http.StatusBadRequest)
		return
	}

	// Обновляем название серии во всех книгах, а также lower-поле
	_, err = w.db.Exec("UPDATE books SET series = ?, series_lower = ? WHERE series = ?", newName, strings.ToLower(newName), oldSeriesName)
	if err != nil {
		log.Printf("Database error updating series name from '%s' to '%s': %v", oldSeriesName, newName, err)
		http.Error(wr, "Ошибка сохранения изменений", http.StatusInternalServerError)
		return
	}

	// Возвращаем успешный ответ
	wr.WriteHeader(http.StatusOK)
	wr.Write([]byte("OK"))
}
