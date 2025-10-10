// web/book_edit.go
package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"turanga/config"
	"turanga/scanner"
)

// SaveBookFieldHandler обрабатывает сохранение изменений полей книги
// URL: /save/book/{id} или /save/book/{id}/cover
func (w *WebInterface) SaveBookFieldHandler(wr http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Проверяем, является ли это запросом на загрузку обложки
	path := strings.TrimPrefix(r.URL.Path, "/save/book/")
	parts := strings.SplitN(path, "/", 2) // Разделяем на ID и потенциально "cover"

	if len(parts) >= 2 && parts[1] == "cover" {
		// Это запрос на загрузку обложки
		bookID, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(wr, "Invalid book ID", http.StatusBadRequest)
			return
		}
		// Вызываем функцию загрузки обложки
		w.saveBookCover(wr, r, bookID)
		return
	}

	// Извлекаем ID книги из URL
	path = strings.TrimPrefix(r.URL.Path, "/save/book/")
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

	// Получаем данные из формы
	fieldName := strings.TrimSpace(r.FormValue("field"))
	value := strings.TrimSpace(r.FormValue("value"))

	// Валидация поля
	validFields := map[string]bool{
		"title":      true,
		"authors":    true,
		"series":     true,
		"year":       true,
		"publisher":  true,
		"isbn":       true,
		"annotation": true,
		"tags":       true,
		"over18":     true,
	}

	if !validFields[fieldName] {
		http.Error(wr, "Invalid field name", http.StatusBadRequest)
		return
	}

	// Специальная обработка для тегов
	if fieldName == "tags" {
		action := strings.TrimSpace(r.FormValue("action"))
		switch action {
		case "add":
			err = w.addTagToBook(bookID, value)
		case "remove":
			err = w.removeTagFromBook(bookID, value)
		default:
			// Для совместимости с режимом редактирования
			err = w.updateBookTags(bookID, value)
		}
		if err != nil {
			log.Printf("Database error updating book tags for ID %d: %v", bookID, err)
			http.Error(wr, "Ошибка сохранения тегов", http.StatusInternalServerError)
			return
		}
		wr.WriteHeader(http.StatusOK)
		wr.Write([]byte("OK"))
		return
	}

	// Обновляем соответствующее поле в базе данных
	err = w.updateBookField(bookID, fieldName, value)
	if err != nil {
		log.Printf("Database error updating book field %s for ID %d: %v", fieldName, bookID, err)
		http.Error(wr, "Ошибка сохранения изменений", http.StatusInternalServerError)
		return
	}

	// Возвращаем успешный ответ
	wr.WriteHeader(http.StatusOK)
	wr.Write([]byte("OK"))
}

// updateBookField обновляет конкретное поле книги
func (w *WebInterface) updateBookField(bookID int, fieldName, value string) error {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Printf("updateBookField: bookID=%d, fieldName=%s, value='%s'", bookID, fieldName, value)
	}

	switch fieldName {
	case "title":
		// Обновляем также lower-поле
		_, err := w.db.Exec("UPDATE books SET title = ?, title_lower = ? WHERE id = ?", value, strings.ToLower(value), bookID)
		return err
	case "authors":
		return w.updateBookAuthors(bookID, value)
	case "series":
		// Разбираем значение серии (название|номер)
		parts := strings.Split(value, "|")
		seriesName := ""
		seriesNumber := ""
		if len(parts) > 0 {
			seriesName = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			seriesNumber = strings.TrimSpace(parts[1])
		}

		// Если название серии пустое, обнуляем оба поля
		if seriesName == "" {
			seriesNumber = ""
		}

		if cfg.Debug {
			log.Printf("Updating series: name='%s', number='%s'", seriesName, seriesNumber)
		}
		// Обновляем также lower-поле
		_, err := w.db.Exec("UPDATE books SET series = ?, series_lower = ? WHERE id = ?", seriesName, strings.ToLower(seriesName), bookID)
		return err
	case "series_number":
		// Отдельная обработка номера серии (на случай прямого вызова)
		if cfg.Debug {
			log.Printf("Updating series_number directly: '%s'", value)
		}
		_, err := w.db.Exec("UPDATE books SET series_number = ? WHERE id = ?", value, bookID)
		return err
	case "year":
		_, err := w.db.Exec("UPDATE books SET year = ? WHERE id = ?", value, bookID)
		return err
	case "publisher":
		_, err := w.db.Exec("UPDATE books SET publisher = ? WHERE id = ?", value, bookID)
		return err
	case "isbn":
		_, err := w.db.Exec("UPDATE books SET isbn = ? WHERE id = ?", value, bookID)
		return err
	case "annotation":
		return w.updateBookAnnotation(bookID, value)
	case "tags":
		return w.updateBookTags(bookID, value)
	case "over18":
		over18 := value == "true" || value == "1" || value == "on"
		return w.updateBookOver18(bookID, over18)
	default:
		return fmt.Errorf("unsupported field: %s", fieldName)
	}
}

// updateBookAuthors обновляет авторов книги
func (w *WebInterface) updateBookAuthors(bookID int, authorsStr string) error {
	cfg := config.GetConfig()

	// Начинаем транзакцию
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Сохраняем ID старых авторов этой книги для последующей проверки на "сиротство"
	type oldAuthorInfo struct {
		ID   int
		Name string // Используем full_name для идентификации
	}
	var oldAuthors []oldAuthorInfo
	oldAuthorRows, err := tx.Query(`
		SELECT a.id, a.full_name
		FROM authors a
		JOIN book_authors ba ON a.id = ba.author_id
		WHERE ba.book_id = ?`, bookID)
	if err != nil {
		return fmt.Errorf("ошибка получения старых авторов книги: %w", err)
	}
	defer oldAuthorRows.Close()

	for oldAuthorRows.Next() {
		var oldAuthor oldAuthorInfo
		if err := oldAuthorRows.Scan(&oldAuthor.ID, &oldAuthor.Name); err != nil {
			return fmt.Errorf("ошибка сканирования старого автора: %w", err)
		}
		oldAuthors = append(oldAuthors, oldAuthor)
	}
	if err := oldAuthorRows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по старым авторам: %w", err)
	}

	// Удаляем существующие связи авторов для этой книги
	_, err = tx.Exec("DELETE FROM book_authors WHERE book_id = ?", bookID)
	if err != nil {
		return fmt.Errorf("ошибка удаления старых связей авторов: %w", err)
	}

	// Если строка авторов пуста, просто коммитим (после проверки сирот) и выходим
	if authorsStr == "" {
		// Проверяем "сирот" среди старых авторов и удаляем их
		// ... (логика проверки сирот остается без изменений)
		// Проверяем "сирот" среди старых авторов и удаляем их
		for _, oldAuthor := range oldAuthors {
			var bookCount int
			err = tx.QueryRow("SELECT COUNT(*) FROM book_authors WHERE author_id = ?", oldAuthor.ID).Scan(&bookCount)
			if err != nil {
				if cfg.Debug {
					log.Printf("Предупреждение: ошибка проверки количества книг у автора %d (%s): %v", oldAuthor.ID, oldAuthor.Name, err)
				}
				continue // Продолжаем, чтобы не прервать транзакцию из-за предупреждения
			}
			if bookCount == 0 {
				// Автор не связан ни с одной книгой, удаляем его
				_, err = tx.Exec("DELETE FROM authors WHERE id = ?", oldAuthor.ID)
				if err != nil {
					if cfg.Debug {
						log.Printf("Предупреждение: ошибка удаления сиротского автора %d (%s): %v", oldAuthor.ID, oldAuthor.Name, err)
					}
				} else {
					if cfg.Debug {
						log.Printf("Удален сиротский автор: %s (ID: %d)", oldAuthor.Name, oldAuthor.ID)
					}
				}
			}
		}
		return tx.Commit()
	}

	// Собираем ID новых авторов для этой книги
	var newAuthorIDs []int

	// Разбираем авторов
	authorNames := strings.Split(authorsStr, ",")
	for _, authorName := range authorNames {
		authorName = strings.TrimSpace(authorName)
		if authorName == "" {
			continue
		}

		// Ищем автора с таким именем (full_name)
		var authorID int
		err = tx.QueryRow("SELECT id FROM authors WHERE full_name = ?", authorName).Scan(&authorID)
		if err != nil {
			if err == sql.ErrNoRows {
				// Создаем нового автора
				// Вычисляем last_name_lower как последнее слово в authorName, приведенное к нижнему регистру
				var lastNameLower string
				if parts := strings.Fields(authorName); len(parts) > 0 {
					lastNameLower = strings.ToLower(parts[len(parts)-1])
				} else {
					lastNameLower = strings.ToLower(authorName)
				}
				// Обновляем также full_name_lower
				result, err := tx.Exec("INSERT INTO authors (last_name_lower, full_name, full_name_lower) VALUES (?, ?, ?)",
					lastNameLower, authorName, strings.ToLower(authorName))
				if err != nil {
					return fmt.Errorf("ошибка создания нового автора '%s': %w", authorName, err)
				}
				authorID64, err := result.LastInsertId()
				if err != nil {
					return fmt.Errorf("ошибка получения ID нового автора '%s': %w", authorName, err)
				}
				authorID = int(authorID64)
				if cfg.Debug {
					log.Printf("Создан новый автор: %s (ID: %d, last_name_lower: %s)", authorName, authorID, lastNameLower)
				}
			} else {
				return fmt.Errorf("ошибка поиска автора '%s': %w", authorName, err)
			}
		} else {
			if cfg.Debug {
				log.Printf("Найден существующий автор: %s (ID: %d)", authorName, authorID)
			}
		}
		// Добавляем ID автора в список
		newAuthorIDs = append(newAuthorIDs, authorID)
	}

	// Создаем связи книга-автор для новых авторов
	for _, authorID := range newAuthorIDs {
		_, err = tx.Exec("INSERT INTO book_authors (book_id, author_id) VALUES (?, ?)", bookID, authorID)
		if err != nil {
			return fmt.Errorf("ошибка создания связи книга-автор (%d-%d): %w", bookID, authorID, err)
		}
	}

	// Проверяем "сирот" среди старых авторов и удаляем их
	// Создаем map для быстрого поиска новых авторов
	newAuthorIDMap := make(map[int]bool)
	for _, id := range newAuthorIDs {
		newAuthorIDMap[id] = true
	}

	for _, oldAuthor := range oldAuthors {
		// Если старый автор не в списке новых авторов, проверяем на "сиротство"
		if !newAuthorIDMap[oldAuthor.ID] {
			var bookCount int
			err = tx.QueryRow("SELECT COUNT(*) FROM book_authors WHERE author_id = ?", oldAuthor.ID).Scan(&bookCount)
			if err != nil {
				if cfg.Debug {
					log.Printf("Предупреждение: ошибка проверки количества книг у старого автора %d (%s): %v",
						oldAuthor.ID, oldAuthor.Name, err)
				}
				continue
			}
			if bookCount == 0 {
				// Автор не связан ни с одной книгой, удаляем его
				_, err = tx.Exec("DELETE FROM authors WHERE id = ?", oldAuthor.ID)
				if err != nil {
					if cfg.Debug {
						log.Printf("Предупреждение: ошибка удаления сиротского автора %d (%s): %v", oldAuthor.ID, oldAuthor.Name, err)
					}
				} else {
					if cfg.Debug {
						log.Printf("Удален сиротский автор: %s (ID: %d)", oldAuthor.Name, oldAuthor.ID)
					}
				}
			}
		}
	}

	return tx.Commit()
}

// updateBookTags обновляет теги книги
func (w *WebInterface) updateBookTags(bookID int, tagsStr string) error {
	// Начинаем транзакцию
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Удаляем существующие связи тегов
	_, err = tx.Exec("DELETE FROM book_tags WHERE book_id = ?", bookID)
	if err != nil {
		return err
	}

	// Если строка тегов пуста, завершаем
	if tagsStr == "" {
		return tx.Commit()
	}

	// Разбираем теги
	tagNames := strings.Split(tagsStr, ",")
	for _, tagName := range tagNames {
		tagName = strings.TrimSpace(tagName)
		if tagName == "" {
			continue
		}

		// Ограничиваем длину тега
		if len(tagName) > 16 {
			tagName = tagName[:16]
		}

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

		// Создаем связь книга-тег
		_, err = tx.Exec("INSERT INTO book_tags (book_id, tag_id) VALUES (?, ?)", bookID, tagID)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// updateBookOver18 обновляет флаг over18 для книги
func (w *WebInterface) updateBookOver18(bookID int, over18 bool) error {
	_, err := w.db.Exec("UPDATE books SET over18 = ? WHERE id = ?", over18, bookID)
	return err
}

// updateBookAnnotation обновляет аннотацию книги, сохраняя её в файл
func (w *WebInterface) updateBookAnnotation(bookID int, annotation string) error {
	// Получаем хеш файла книги для формирования имени файла аннотации
	var fileHash sql.NullString
	err := w.db.QueryRow("SELECT file_hash FROM books WHERE id = ?", bookID).Scan(&fileHash)
	if err != nil {
		return fmt.Errorf("ошибка получения хеша книги: %w", err)
	}

	if !fileHash.Valid || fileHash.String == "" {
		return fmt.Errorf("у книги нет хеша файла")
	}

	// Сохраняем аннотацию в файл
	return w.saveAnnotationToFile(bookID, annotation, fileHash.String)
}

// saveAnnotationToFile сохраняет аннотацию в файл
func (w *WebInterface) saveAnnotationToFile(bookID int, annotation string, fileHash string) error {
	cfg := config.GetConfig()

	// Определяем каталог notes относительно каталога программы
	notesDir := filepath.Join(w.rootPath, "notes")

	// Создаем каталог если он не существует
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		return fmt.Errorf("ошибка создания каталога notes: %w", err)
	}

	// Формируем имя файла аннотации
	noteFileName := fmt.Sprintf("%s.txt", fileHash)
	noteFilePath := filepath.Join(notesDir, noteFileName)

	// Сохраняем аннотацию в файл
	if annotation != "" {
		err := os.WriteFile(noteFilePath, []byte(annotation), 0644)
		if err != nil {
			return fmt.Errorf("ошибка сохранения аннотации в файл: %w", err)
		}
	} else {
		// Если аннотация пустая, удаляем файл
		if _, err := os.Stat(noteFilePath); err == nil {
			err := os.Remove(noteFilePath)
			if err != nil {
				if cfg.Debug {
					log.Printf("Предупреждение: ошибка удаления пустого файла аннотации %s: %v", noteFilePath, err)
				}
			}
		}
	}

	return nil
}

// saveBookCover обрабатывает загрузку новой обложки для книги
func (w *WebInterface) saveBookCover(wr http.ResponseWriter, r *http.Request, bookID int) {
	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Только POST запросы
	if r.Method != http.MethodPost {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Получаем file_hash из формы
	fileHash := r.FormValue("file_hash")
	if fileHash == "" {
		http.Error(wr, "file_hash не предоставлен", http.StatusBadRequest)
		return
	}

	// Получаем файл из формы
	file, handler, err := r.FormFile("cover")
	if err != nil {
		http.Error(wr, "Файл обложки не найден в запросе: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Простая проверка MIME-типа (дополнительно можно проверить содержимое файла)
	contentType := handler.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		// Можно добавить более строгую проверку по сигнатуре файла
		http.Error(wr, "Загруженный файл должен быть изображением", http.StatusBadRequest)
		return
	}

	// Декодируем изображение из загруженного файла
	//	img, format, err := image.Decode(file)
	img, _, err := image.Decode(file)
	if err != nil {
		log.Printf("Ошибка декодирования загруженного изображения: %v", err)
		http.Error(wr, "Файл не является корректным изображением", http.StatusBadRequest)
		return
	}
	//	log.Printf("Загружено изображение формата: %s", format)

	// Вызываем функцию из scanner пакета для изменения размера и сохранения
	coverURL, err := scanner.ProcessAndSaveCoverImage(img, fileHash)
	if err != nil {
		log.Printf("Ошибка обработки и сохранения обложки через scanner: %v", err)
		http.Error(wr, "Ошибка обработки обложки: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Возвращаем JSON ответ
	wr.Header().Set("Content-Type", "application/json")
	// Используем map[string]interface{} для корректного JSON ответа
	json.NewEncoder(wr).Encode(map[string]interface{}{
		"success":   true,
		"cover_url": coverURL,
		"message":   "Обложка успешно загружена и обработана",
	})
}
