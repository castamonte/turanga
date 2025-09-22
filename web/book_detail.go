// web/book_detail.go
package web

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"turanga/config"
	"turanga/models"
)

// ShowBookDetailHandler обрабатывает запросы к странице деталей книги
// URL: /book/{id}
func (w *WebInterface) ShowBookDetailHandler(wr http.ResponseWriter, r *http.Request) {
	// Извлекаем ID книги из URL
	// Ожидаем URL вида /book/123
	path := strings.TrimPrefix(r.URL.Path, "/book/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Book ID is required", http.StatusBadRequest)
		return
	}
	bookID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(wr, "Invalid book ID", http.StatusBadRequest)
		return
	}

	// Получаем данные книги из БД
	// Используем подзапросы для корректного получения авторов и тегов
	row := w.db.QueryRow(`
	SELECT b.id as book_id, b.title, b.file_hash, b.over18, b.ipfs_cid,
		   (SELECT GROUP_CONCAT(a.full_name, ', ') 
			FROM book_authors ba 
			LEFT JOIN authors a ON ba.author_id = a.id 
			WHERE ba.book_id = b.id) as authors_str,
		   b.series, b.series_number, b.published_at, 
		   b.isbn, b.year, b.publisher,
		   b.file_url, b.file_type, b.file_hash, b.file_size,
		   (SELECT GROUP_CONCAT(t.name, ', ') 
			FROM book_tags bt 
			LEFT JOIN tags t ON bt.tag_id = t.id 
			WHERE bt.book_id = b.id) as tags_str
	FROM books b 
	WHERE b.id = ?
	`, bookID)

	// Получаем авторов с ID для создания ссылок
	var authors []models.AuthorInfo
	authorRows, err := w.db.Query(`
    	SELECT a.id, a.full_name 
    	FROM authors a
    	JOIN book_authors ba ON a.id = ba.author_id
    	WHERE ba.book_id = ?
    	ORDER BY a.last_name, a.full_name
	`, bookID)
	if err == nil {
		defer authorRows.Close()
		for authorRows.Next() {
			var author models.AuthorInfo
			if err := authorRows.Scan(&author.ID, &author.Name); err == nil {
				authors = append(authors, author)
			}
		}
		authorRows.Close()
	}

	var b models.BookWeb
	// Обновлён список переменных для Scan
	var isbn, year, publisher, series, seriesNumber sql.NullString
	var fileSize sql.NullInt64
	var id int
	var authorsStr sql.NullString // ИЗМЕНЕНО: теперь sql.NullString вместо string
	var tagsStr sql.NullString
	var fileType, fileHash, ipfsCID sql.NullString
	var fileURL sql.NullString
	var over18 sql.NullBool

	err = row.Scan(&id, &b.Title, &fileHash, &over18, &ipfsCID, &authorsStr, &series, &seriesNumber, &b.PublishedAt,
		&isbn, &year, &publisher,
		&fileURL, &fileType, &fileHash, &fileSize, &tagsStr)

	// Обработку over18:
	if over18.Valid {
		b.Over18 = over18.Bool
	}

	// Проверку доступа для книг 18+:
	if b.Over18 && !w.isAuthenticated(r) {
		http.Error(wr, "Доступ к этой книге ограничен", http.StatusForbidden)
		return
	}
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(wr, "Book not found", http.StatusNotFound)
		} else {
			log.Printf("Database error getting book details for ID %d: %v", bookID, err)
			http.Error(wr, "Database error", http.StatusInternalServerError)
		}
		return
	}

	b.ID = id
	// Обработка fileHash для получения обложки и аннотации:
	if fileHash.Valid {
		b.FileHash = fileHash.String
		// Получаем путь к обложке по хешу
		b.CoverURL = w.getCoverURLFromFileHash(fileHash.String)
		// Читаем аннотацию из файла
		b.Annotation = w.getAnnotationFromFile(b.ID, fileHash.String)
	} else {
		b.CoverURL = ""
	}

	if ipfsCID.Valid {
		b.IPFS_CID = ipfsCID.String
	}

	if isbn.Valid {
		b.ISBN = isbn.String
	}
	if year.Valid {
		b.Year = year.String
	}
	if publisher.Valid {
		b.Publisher = publisher.String
	}
	if series.Valid {
		b.Series = series.String
	}
	if seriesNumber.Valid {
		b.SeriesNumber = seriesNumber.String
	}

	// Обработка authorsStr с проверкой на Valid
	if authorsStr.Valid && authorsStr.String != "" {
		b.AuthorsStr = authorsStr.String
	} else {
		b.AuthorsStr = "Автор не указан"
	}

	if tagsStr.Valid {
		b.TagsStr = tagsStr.String
	}
	if fileSize.Valid {
		b.FileSize = fileSize.Int64
	}

	// Обрабатываем файл книги
	if fileURL.Valid && fileType.Valid {
		// Формируем правильный URL для скачивания через OPDS
		downloadURL := fmt.Sprintf("/opds-download/%d/", id)

		fileWeb := models.BookFileWeb{
			URL:      downloadURL, // <-- Здесь правильный URL для скачивания
			Type:     fileType.String,
			FileHash: fileHash.String,
		}
		if fileSize.Valid {
			fileWeb.FileSize = fileSize.Int64
		}
		b.Files = append(b.Files, fileWeb)
	}

	// Подготавливаем данные для шаблона
	fileTypeStr := ""
	if fileType.Valid {
		fileTypeStr = fileType.String
	}

	data := struct {
		Book            *models.BookWeb
		Authors         []models.AuthorInfo
		IsAuthenticated bool
		Title           string
		FileType        string
		IPFSGateway     string
	}{
		Book:            &b,
		Authors:         authors,
		IsAuthenticated: w.isAuthenticated(r),
		Title:           b.Title,
		FileType:        fileTypeStr,
		IPFSGateway:     w.config.GetIPFSGateway(),
	}

	//log.Printf("IPFS Gateway from config: %s", w.config.GetIPFSGateway())

	// Создаем шаблон и добавляем вспомогательные функции
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "book_detail.html")
	tmpl, err := template.New("book_detail.html").Funcs(template.FuncMap{
		"upper":      strings.ToUpper,
		"split":      strings.Split,
		"trim":       strings.TrimSpace,
		"urlquery":   url.QueryEscape,
		"formatSize": FormatFileSize,
	}).ParseFiles(tmplPath)

	if err != nil {
		log.Printf("Error parsing templates: %v", err)
		http.Error(wr, "Template error", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон
	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(wr, data); err != nil {
		log.Printf("Error executing book detail template for ID %d: %v", bookID, err)
		http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// addTagToBook добавляет тег к книге
func (w *WebInterface) addTagToBook(bookID int, tagName string) error {
	tagName = strings.TrimSpace(tagName)
	if tagName == "" {
		return nil
	}

	// Ограничиваем длину тега
	if len(tagName) > 16 {
		tagName = tagName[:16]
	}

	// Начинаем транзакцию
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Ищем или создаем тег
	var tagID int
	err = tx.QueryRow("SELECT id FROM tags WHERE name = ?", tagName).Scan(&tagID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Создаем новый тег
			result, err := tx.Exec("INSERT INTO tags (name) VALUES (?)", tagName)
			if err != nil {
				return err
			}
			tagID64, err := result.LastInsertId()
			if err != nil {
				return err
			}
			tagID = int(tagID64)
		} else {
			return err
		}
	}

	// Проверяем, есть ли уже такая связь
	var exists int
	err = tx.QueryRow("SELECT COUNT(*) FROM book_tags WHERE book_id = ? AND tag_id = ?", bookID, tagID).Scan(&exists)
	if err != nil {
		return err
	}

	// Если связи нет, создаем её
	if exists == 0 {
		_, err = tx.Exec("INSERT INTO book_tags (book_id, tag_id) VALUES (?, ?)", bookID, tagID)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// removeTagFromBook удаляет тег у книги
func (w *WebInterface) removeTagFromBook(bookID int, tagName string) error {
	tagName = strings.TrimSpace(tagName)
	if tagName == "" {
		return nil
	}

	// Начинаем транзакцию
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Находим ID тега
	var tagID int
	err = tx.QueryRow("SELECT id FROM tags WHERE name = ?", tagName).Scan(&tagID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Тег не найден - ничего не делаем
			return nil
		}
		return err
	}

	// Удаляем связь книга-тег
	_, err = tx.Exec("DELETE FROM book_tags WHERE book_id = ? AND tag_id = ?", bookID, tagID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteBookHandler обрабатывает удаление книги
func (w *WebInterface) DeleteBookHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Извлекаем ID книги из URL
	path := strings.TrimPrefix(r.URL.Path, "/delete/book/")
	if path == "" || path == r.URL.Path {
		http.Error(wr, "Book ID is required", http.StatusBadRequest)
		return
	}
	bookID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(wr, "Invalid book ID", http.StatusBadRequest)
		return
	}

	// Только POST запросы
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	//	log.Printf("Начало блока IPFS удаления для книги ID %d", bookID)
	var bookIPFSCID sql.NullString
	// Получаем IPFS CID до удаления записи из БД
	if cfg.Debug {
		log.Printf("Проверка конфигурации: w.config != nil: %t, w.config.RemoveFromIPFSOnDelete: %t",
			w.config != nil, w.config.RemoveFromIPFSOnDelete)
	}

	if w.config != nil && w.config.RemoveFromIPFSOnDelete {
		if cfg.Debug {
			log.Printf("Условие w.config != nil && w.config.RemoveFromIPFSOnDelete выполнено, продолжаем")
		}
		err = w.db.QueryRow("SELECT ipfs_cid FROM books WHERE id = ?", bookID).Scan(&bookIPFSCID)
		if cfg.Debug {
			log.Printf("Попытка получения IPFS CID для книги ID %d. Ошибка: %v, Valid: %t, Value: '%s'",
				bookID, err, bookIPFSCID.Valid, bookIPFSCID.String)
		}

		if err != nil && err != sql.ErrNoRows {
			if cfg.Debug {
				log.Printf("Database error getting IPFS CID for book ID %d: %v", bookID, err)
			}
			// Не останавливаем удаление из-за ошибки получения CID
		} else if err == nil && bookIPFSCID.Valid && bookIPFSCID.String != "" {
			// CID найден и валиден
			if cfg.Debug {
				log.Printf("CID найден и валиден. Пробуем удалить из ipfs")
			}
			ipfsShell, shellErr := w.config.GetIPFSShell()
			if shellErr == nil {
				// Открепляем (Unpin) CID из IPFS
				unpinErr := ipfsShell.Unpin(bookIPFSCID.String)
				if unpinErr != nil {
					if cfg.Debug {
						log.Printf("Warning: Failed to unpin IPFS CID %s for book ID %d: %v", bookIPFSCID.String, bookID, unpinErr)
					}
					// Не возвращаем ошибку HTTP, просто логируем предупреждение
				} else {
					if cfg.Debug {
						log.Printf("Successfully unpinned IPFS CID %s for book ID %d", bookIPFSCID.String, bookID)
					}
				}
			} else {
				if cfg.Debug {
					log.Printf("Warning: Failed to get IPFS shell for unpinning CID %s: %v", bookIPFSCID.String, shellErr)
				}
			}
		} else if err == nil {
			if cfg.Debug {
				log.Printf("IPFS CID для книги ID %d не найден или пуст (Valid: %t, Value: '%s')", bookID, bookIPFSCID.Valid, bookIPFSCID.String)
			}
		}
		// Если bookIPFSCID не Valid или пустой, просто продолжаем
	} else {
		if cfg.Debug {
			log.Printf("Условие w.config != nil && w.config.RemoveFromIPFSOnDelete НЕ выполнено, пропускаем блок IPFS")
		}
	}

	// Получаем информацию о книге перед удалением (для удаления файлов)
	var fileURL, fileHash sql.NullString
	err = w.db.QueryRow("SELECT file_url, file_hash FROM books WHERE id = ?", bookID).Scan(&fileURL, &fileHash)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(wr, "Book not found", http.StatusNotFound)
		} else {
			log.Printf("Database error getting book info for ID %d: %v", bookID, err)
			http.Error(wr, "Database error", http.StatusInternalServerError)
		}
		return
	}

	// Начинаем транзакцию
	tx, err := w.db.Begin()
	if err != nil {
		log.Printf("Database error starting transaction for book ID %d: %v", bookID, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Удаляем запись из базы данных (CASCADE удалит связанные записи)
	_, err = tx.Exec("DELETE FROM books WHERE id = ?", bookID)
	if err != nil {
		log.Printf("Database error deleting book ID %d: %v", bookID, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Фиксируем транзакцию
	err = tx.Commit()
	if err != nil {
		log.Printf("Database error committing transaction for book ID %d: %v", bookID, err)
		http.Error(wr, "Database error", http.StatusInternalServerError)
		return
	}

	// Удаляем файлы с диска (если они есть)
	if fileURL.Valid && fileURL.String != "" {
		// fileURL теперь содержит абсолютный путь к файлу
		filePath := fileURL.String

		// Проверяем существование файла перед удалением
		if _, err := os.Stat(filePath); err == nil {
			err := os.Remove(filePath)
			if err != nil {
				if cfg.Debug {
					log.Printf("Error deleting book file %s: %v", filePath, err)
				}
				// Не возвращаем ошибку, просто логируем
			} else {
				//				if cfg.Debug {
				log.Printf("Deleted book file: %s", filePath)
				//				}
			}
		} else if os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Book file not found (already deleted): %s", filePath)
			}
		} else {
			if cfg.Debug {
				log.Printf("Error checking book file %s: %v", filePath, err)
			}
		}
	}

	// Удаляем обложку с диска
	if fileHash.Valid && fileHash.String != "" {
		w.deleteCoverFileByHash(fileHash.String)
	}

	// Удаляем файл аннотации
	if fileHash.Valid && fileHash.String != "" {
		w.deleteAnnotationFileByHash(fileHash.String)
	}

	// Возвращаем успешный ответ и перенаправляем на главную страницу
	http.Redirect(wr, r, "/", http.StatusSeeOther)
}

// deleteCoverFileByHash удаляет файл обложки по хешу
func (w *WebInterface) deleteCoverFileByHash(fileHash string) {
	cfg := config.GetConfig()

	// Определяем каталог covers в корневой директории приложения
	coversDir := "./covers"
	if w.rootPath != "" {
		coversDir = filepath.Join(w.rootPath, "covers")
	}

	// Возможные расширения обложек
	extensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}

	// Ищем и удаляем файл обложки с любым из расширений
	for _, ext := range extensions {
		coverFileName := fileHash + ext
		coverFilePath := filepath.Join(coversDir, coverFileName)

		if _, err := os.Stat(coverFilePath); err == nil {
			err := os.Remove(coverFilePath)
			if err != nil {
				if cfg.Debug {
					log.Printf("Error deleting cover file %s: %v", coverFilePath, err)
				}
			} else {
				if cfg.Debug {
					log.Printf("Deleted cover file: %s", coverFilePath)
				}
			}
		} else if !os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Error checking cover file %s: %v", coverFilePath, err)
			}
		}
	}
}

// deleteAnnotationFileByHash удаляет файл аннотации по хешу
func (w *WebInterface) deleteAnnotationFileByHash(fileHash string) {
	cfg := config.GetConfig()

	// Определяем каталог notes в корневой директории приложения
	notesDir := "./notes"
	if w.rootPath != "" {
		notesDir = filepath.Join(w.rootPath, "notes")
	}

	// Формируем путь к файлу аннотации
	noteFileName := fileHash + ".txt"
	noteFilePath := filepath.Join(notesDir, noteFileName)

	// Проверяем существование файла перед удалением
	if _, err := os.Stat(noteFilePath); err == nil {
		err := os.Remove(noteFilePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error deleting annotation file %s: %v", noteFilePath, err)
			}
		} else {
			if cfg.Debug {
				log.Printf("Deleted annotation file: %s", noteFilePath)
			}
		}
	} else if !os.IsNotExist(err) {
		if cfg.Debug {
			log.Printf("Error checking annotation file %s: %v", noteFilePath, err)
		}
	}
}
