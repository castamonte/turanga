// web/authors.go

package web

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"turanga/config"
	"turanga/models"
)

// ShowAuthorHandler обрабатывает запросы к странице автора
// URL: /author/{id}
func (w *WebInterface) ShowAuthorHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Извлекаем ID автора из URL
	path := strings.TrimPrefix(r.URL.Path, "/author/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Author ID is required", http.StatusBadRequest)
		return
	}
	authorID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(wr, "Invalid author ID", http.StatusBadRequest)
		return
	}

	// Получаем информацию об авторе, включая last_name_lower
	var authorName, authorLastNameLower string
	err = w.db.QueryRow("SELECT full_name, last_name_lower FROM authors WHERE id = ?", authorID).Scan(&authorName, &authorLastNameLower)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(wr, "Author not found", http.StatusNotFound)
		} else {
			log.Printf("Database error getting author name for ID %d: %v", authorID, err)
			http.Error(wr, "Database error", http.StatusInternalServerError)
		}
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

	// Получаем общее количество книг автора
	var totalBooks int
	err = w.db.QueryRow(`
        SELECT COUNT(*) 
        FROM books b 
        JOIN book_authors ba ON b.id = ba.book_id 
        WHERE ba.author_id = ?
    `, authorID).Scan(&totalBooks)
	if err != nil {
		log.Printf("Database error getting total books count for author %d: %v", authorID, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Получаем книги автора с группировкой по сериям
	rows, err := w.db.Query(`
        SELECT b.id, b.title, b.series, b.series_number,
               (SELECT GROUP_CONCAT(a.full_name, ', ') 
                FROM book_authors ba2 
                LEFT JOIN authors a ON ba2.author_id = a.id 
                WHERE ba2.book_id = b.id) as authors_str,
               b.file_hash
        FROM books b 
        JOIN book_authors ba ON b.id = ba.book_id
        WHERE ba.author_id = ?
        GROUP BY b.id, b.title, b.series, b.series_number, b.file_hash
        ORDER BY 
            CASE WHEN b.series IS NULL OR b.series = '' THEN 1 ELSE 0 END,
            LOWER(b.series),
            CASE 
                WHEN b.series_number GLOB '[0-9]*' THEN CAST(b.series_number AS INTEGER)
                ELSE 0
            END,
            LOWER(b.series_number),
            LOWER(b.title)
        LIMIT ? OFFSET ?
    `, authorID, perPage, offset)
	if err != nil {
		log.Printf("Database error getting author books for ID %d: %v", authorID, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Группируем книги по сериям
	type SeriesGroup struct {
		Name  string
		Books []models.BookWeb
	}

	seriesGroups := make(map[string]*SeriesGroup)
	var seriesOrder []string // Для сохранения порядка серий

	for rows.Next() {
		var b models.BookWeb
		var series, seriesNumber sql.NullString
		var authorsStr string
		var fileHash sql.NullString
		err := rows.Scan(&b.ID, &b.Title, &series, &seriesNumber, &authorsStr, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error scanning author book row: %v", err)
			}
			continue
		}

		if series.Valid {
			b.Series = series.String
		}
		if seriesNumber.Valid {
			b.SeriesNumber = seriesNumber.String
		}
		b.AuthorsStr = authorsStr

		// Получаем URL обложки по хешу
		if fileHash.Valid {
			b.CoverURL = w.getCoverURLFromFileHash(fileHash.String, w.config)
		}

		// Определяем группу (серия или "Без серии")
		seriesKey := "Без серии"
		if series.Valid && series.String != "" {
			seriesKey = series.String
		}

		// Создаем группу, если её нет
		if _, exists := seriesGroups[seriesKey]; !exists {
			seriesGroups[seriesKey] = &SeriesGroup{
				Name: seriesKey,
			}
			seriesOrder = append(seriesOrder, seriesKey)
		}

		// Добавляем книгу в группу
		seriesGroups[seriesKey].Books = append(seriesGroups[seriesGroups[seriesKey].Name].Books, b)
	}

	if err = rows.Err(); err != nil {
		log.Printf("Error iterating author book rows: %v", err)
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
		AuthorName          string
		AuthorLastNameLower string // Новое поле
		SeriesGroups        map[string]*SeriesGroup
		SeriesOrder         []string
		CurrentPage         int
		TotalPages          int
		StartPage           int
		EndPage             int
		PageNumbers         []int
		PrevPage            int
		NextPage            int
		AuthorID            int
		IsAuthenticated     bool
	}{
		AuthorName:          authorName,
		AuthorLastNameLower: authorLastNameLower, // Передаем значение
		SeriesGroups:        seriesGroups,
		SeriesOrder:         seriesOrder,
		CurrentPage:         page,
		TotalPages:          totalPages,
		StartPage:           startPage,
		EndPage:             endPage,
		PrevPage:            page - 1,
		NextPage:            page + 1,
		AuthorID:            authorID,
		IsAuthenticated:     w.isAuthenticated(r),
	}

	// Генерируем список номеров страниц
	for i := startPage; i <= endPage; i++ {
		data.PageNumbers = append(data.PageNumbers, i)
	}

	// Загружаем шаблон
	tmpl, err := w.loadTemplates()
	if err != nil {
		log.Printf("Error loading templates: %v", err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон
	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(wr, "author", data); err != nil {
		log.Printf("Error executing author template for ID %d: %v", authorID, err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if cfg.Debug {
		log.Printf("Отображено %d книг на странице %d из %d (Поиск: '')\n", len(seriesGroups), page, totalPages)
	}
}

// SaveAuthorHandler обрабатывает сохранение изменений автора
func (w *WebInterface) SaveAuthorHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Извлекаем ID автора из URL
	path := strings.TrimPrefix(r.URL.Path, "/save/author/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Author ID is required", http.StatusBadRequest)
		return
	}
	authorID, err := strconv.Atoi(path)
	if err != nil {
		log.Printf("Invalid author ID in URL: %s, error: %v", path, err)
		http.Error(wr, "Invalid author ID", http.StatusBadRequest)
		return
	}

	// Только POST запросы
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем новое имя и фамилию для сортировки из формы
	newName := strings.TrimSpace(r.FormValue("name"))
	newLastNameLowerInput := strings.TrimSpace(r.FormValue("last_name_lower")) // Получаем введенное значение

	if newName == "" {
		http.Error(wr, "Имя автора не может быть пустым", http.StatusBadRequest)
		return
	}

	// Определяем значение last_name_lower для сохранения в БД
	var lastNameLowerToSave string
	if newLastNameLowerInput != "" {
		// Если пользователь ввел значение, используем его (в нижнем регистре)
		lastNameLowerToSave = strings.ToLower(newLastNameLowerInput)
	} else {
		// Если пользователь не ввел значение, вычисляем его из full_name
		nameParts := strings.Fields(newName)
		if len(nameParts) > 0 {
			lastNameLowerToSave = strings.ToLower(nameParts[len(nameParts)-1])
		} else {
			lastNameLowerToSave = strings.ToLower(newName)
		}
	}

	// Проверяем, существует ли уже автор с таким именем (full_name)
	var existingAuthorID int
	err = w.db.QueryRow("SELECT id FROM authors WHERE full_name = ?", newName).Scan(&existingAuthorID)

	if err != nil && err != sql.ErrNoRows {
		// Ошибка базы данных
		log.Printf("Database error checking existing author: %v", err)
		http.Error(wr, "Ошибка базы данных", http.StatusInternalServerError)
		return
	}

	targetAuthorID := authorID

	if err == nil {
		// Автор с таким именем уже существует
		if existingAuthorID != authorID {
			log.Printf("Автор с именем '%s' уже существует (ID: %d), текущий ID: %d", newName, existingAuthorID, authorID)

			// Начинаем транзакцию для атомарного обновления
			tx, err := w.db.Begin()
			if err != nil {
				log.Printf("Ошибка начала транзакции: %v", err)
				http.Error(wr, "Ошибка базы данных", http.StatusInternalServerError)
				return
			}
			// Откатываем транзакцию в случае ошибки, если Commit не будет вызван
			defer func() {
				if err != nil {
					tx.Rollback()
				}
			}()

			// Переносим связи книг от старого автора к новому
			_, err = tx.Exec("UPDATE book_authors SET author_id = ? WHERE author_id = ?", existingAuthorID, authorID)
			if err != nil {
				// defer позаботится об откате
				log.Printf("Ошибка обновления связей книг: %v", err)
				http.Error(wr, "Ошибка обновления связей книг", http.StatusInternalServerError)
				return
			}

			// Удаляем старого автора
			_, err = tx.Exec("DELETE FROM authors WHERE id = ?", authorID)
			if err != nil {
				// defer позаботится об откате
				log.Printf("Ошибка удаления старого автора: %v", err)
				http.Error(wr, "Ошибка удаления старого автора", http.StatusInternalServerError)
				return
			}

			// Коммитим транзакцию
			err = tx.Commit()
			if err != nil {
				log.Printf("Ошибка коммита транзакции: %v", err)
				http.Error(wr, "Ошибка сохранения изменений", http.StatusInternalServerError)
				return
			}

			// Используем существующий ID
			targetAuthorID = existingAuthorID

		} else {
			// Это тот же автор - просто обновляем данные
			// Обновляем также lower-поля
			_, err = w.db.Exec("UPDATE authors SET full_name = ?, last_name_lower = ?, full_name_lower = ? WHERE id = ?",
				newName, lastNameLowerToSave, strings.ToLower(newName), authorID)
			if err != nil {
				log.Printf("Database error updating author (ID: %d, fullName: %s): %v", authorID, newName, err)
				http.Error(wr, "Ошибка сохранения изменений: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	} else {
		// Автор не существует - обновляем текущего автора
		// Обновляем также lower-поля
		_, err = w.db.Exec("UPDATE authors SET full_name = ?, last_name_lower = ?, full_name_lower = ? WHERE id = ?",
			newName, lastNameLowerToSave, strings.ToLower(newName), authorID)
		if err != nil {
			log.Printf("Database error updating author (ID: %d, fullName: %s): %v", authorID, newName, err)
			http.Error(wr, "Ошибка сохранения изменений: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Возвращаем успешный ответ с ID целевого автора
	wr.Header().Set("Content-Type", "text/plain; charset=utf-8")
	wr.WriteHeader(http.StatusOK)
	wr.Write([]byte(fmt.Sprintf("OK:%d", targetAuthorID)))
}
