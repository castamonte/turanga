// web/upload.go
package web

import (
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"turanga/config"
	"turanga/scanner"
)

// UploadHandler обрабатывает загрузку файлов
func (w *WebInterface) UploadHandler(wr http.ResponseWriter, r *http.Request) {
	//	log.Printf("UploadHandler called")

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		log.Printf("Upload attempt by unauthorized user")
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		log.Printf("Method not allowed: %s", r.Method)
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Парсим multipart форму
	err := r.ParseMultipartForm(32 << 20) // 32MB max memory
	if err != nil {
		log.Printf("Error parsing multipart form: %v", err)
		http.Error(wr, "Error parsing upload data", http.StatusBadRequest)
		return
	}

	// Обрабатываем загрузку книги
	w.handleBookUpload(wr, r)
}

// UploadBookHandler обрабатывает загрузку книг
func (w *WebInterface) UploadBookHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Printf("UploadBookHandler called: method=%s, path=%s", r.Method, r.URL.Path)
	}

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		log.Printf("Upload attempt by unauthorized user")
		http.Error(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Показываем форму загрузки
		w.showUploadForm(wr, r)
	case http.MethodPost:
		// Обрабатываем загрузку
		w.handleBookUpload(wr, r)
	default:
		log.Printf("Method not allowed: %s", r.Method)
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// showUploadForm показывает форму загрузки книги (содержимое модального окна)
func (w *WebInterface) showUploadForm(wr http.ResponseWriter, r *http.Request) {
	//	log.Printf("Loading upload template from web/templates/upload.html")

	// Загружаем шаблон из файла
	tmplPath := filepath.Join(w.rootPath, "web", "templates", "upload.html")
	tmpl, err := template.New("upload.html").Funcs(template.FuncMap{
		"split":      strings.Split,
		"trim":       strings.TrimSpace,
		"formatSize": FormatFileSize, // Добавлено, на случай если понадобится
	}).ParseFiles(tmplPath)

	if err != nil {
		log.Printf("Error parsing upload template: %v", err)
		http.Error(wr, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Выполняем именованный шаблон "upload"
	if err := tmpl.ExecuteTemplate(wr, "upload", nil); err != nil {
		log.Printf("Error executing upload template: %v", err)
		http.Error(wr, "Internal Server Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// log.Printf("Upload form (modal content) rendered successfully")
}

// handleBookUpload обрабатывает загрузку файла книги
func (w *WebInterface) handleBookUpload(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	//	log.Printf("Starting handleBookUpload")

	// Парсим multipart форму если еще не разобрана
	if r.MultipartForm == nil {
		err := r.ParseMultipartForm(32 << 20) // 32MB max memory
		if err != nil {
			log.Printf("Error parsing multipart form: %v", err)
			http.Error(wr, "Error parsing upload data", http.StatusBadRequest)
			return
		}
	}

	// Определяем, есть ли множественные файлы
	var uploadedFiles []*multipart.FileHeader

	// Сначала проверяем множественные файлы (новый формат)
	if bookFiles := r.MultipartForm.File["book_files"]; len(bookFiles) > 0 {
		uploadedFiles = bookFiles
		if cfg.Debug {
			log.Printf("Found %d files in book_files[]", len(bookFiles))
		}
	} else if bookFile := r.MultipartForm.File["book_file"]; len(bookFile) > 0 {
		// Поддержка старого формата для совместимости
		uploadedFiles = bookFile
		if cfg.Debug {
			log.Printf("Found 1 file in book_file")
		}
	} else {
		if cfg.Debug {
			log.Printf("No files found in upload")
		}
		http.Error(wr, "No files uploaded", http.StatusBadRequest)
		return
	}

	if cfg.Debug {
		log.Printf("Processing %d uploaded files", len(uploadedFiles))
	}

	successCount := 0
	errorCount := 0
	var firstError error

	// Обрабатываем каждый файл
	for i, fileHeader := range uploadedFiles {
		if cfg.Debug {
			log.Printf("Processing file %d: %s (size: %d bytes)", i+1, fileHeader.Filename, fileHeader.Size)
		}

		// Проверяем размер файла
		if fileHeader.Size > maxFileSize {
			if cfg.Debug {
				log.Printf("File %s too large: %d bytes", fileHeader.Filename, fileHeader.Size)
			}
			errorCount++
			if firstError == nil {
				firstError = fmt.Errorf("file %s too large", fileHeader.Filename)
			}
			continue
		}

		// Открываем файл
		file, err := fileHeader.Open()
		if err != nil {
			if cfg.Debug {
				log.Printf("Error opening file %s: %v", fileHeader.Filename, err)
			}
			errorCount++
			if firstError == nil {
				if cfg.Debug {
					firstError = fmt.Errorf("error opening file %s: %v", fileHeader.Filename, err)
				}
			}
			continue
		}
		defer file.Close()

		// Создаем временный файл
		tempFile, err := ioutil.TempFile("", "upload_*_"+filepath.Base(fileHeader.Filename))
		if err != nil {
			if cfg.Debug {
				log.Printf("Error creating temp file for %s: %v", fileHeader.Filename, err)
			}
			errorCount++
			if firstError == nil {
				firstError = fmt.Errorf("error creating temp file for %s: %v", fileHeader.Filename, err)
			}
			continue
		}
		tempPath := tempFile.Name()
		defer func() {
			tempFile.Close()
			os.Remove(tempPath)
		}()

		// Копируем содержимое в временный файл
		_, err = io.Copy(tempFile, file)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error copying file %s: %v", fileHeader.Filename, err)
			}
			errorCount++
			if firstError == nil {
				firstError = fmt.Errorf("error copying file %s: %v", fileHeader.Filename, err)
			}
			continue
		}

		// Закрываем временный файл перед обработкой
		tempFile.Close()

		// Получаем информацию о файле
		fileInfo, err := os.Stat(tempPath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error getting file info for %s: %v", fileHeader.Filename, err)
			}
			errorCount++
			if firstError == nil {
				firstError = fmt.Errorf("error getting file info for %s: %v", fileHeader.Filename, err)
			}
			continue
		}

		// Обрабатываем загруженную книгу
		err = w.processUploadedBook(tempPath, fileInfo, fileHeader.Filename)
		if err != nil {
			if cfg.Debug {
				log.Printf("Error processing book %s: %v", fileHeader.Filename, err)
			}
			errorCount++
			if firstError == nil {
				firstError = fmt.Errorf("error processing book %s: %v", fileHeader.Filename, err)
			}
			continue
		}

		if cfg.Debug {
			log.Printf("Successfully processed file %s", fileHeader.Filename)
		}
		successCount++
	}

	// Возвращаем результат
	if errorCount == 0 {
		if cfg.Debug {
			log.Printf("All %d files uploaded successfully", successCount)
		}
		w.writeJSONResponse(wr, map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Successfully uploaded %d book(s)", successCount),
			"count":   successCount,
		})
	} else {
		log.Printf("Uploaded: %d successful, %d errors", successCount, errorCount)
		errorMessage := fmt.Sprintf("Uploaded: %d successful, %d errors", successCount, errorCount)
		if firstError != nil {
			errorMessage += fmt.Sprintf(". First error: %v", firstError)
		}
		if successCount > 0 {
			// Частичный успех
			w.writeJSONResponse(wr, map[string]interface{}{
				"success":      true,
				"partial":      true,
				"message":      errorMessage,
				"successCount": successCount,
				"errorCount":   errorCount,
			})
		} else {
			// Полный провал
			http.Error(wr, errorMessage, http.StatusBadRequest)
		}
	}
}

// processUploadedBook обрабатывает загруженный файл книги
func (w *WebInterface) processUploadedBook(filePath string, fileInfo os.FileInfo, originalName string) error {
	cfg := config.GetConfig()

	// Устанавливаем соединение с БД и конфигурацию для scanner
	scanner.SetDB(w.db)
	if w.config != nil {
		scanner.SetConfig(w.config)
	}

	// Определяем каталог books - используем ту же логику, что и в getCoverURLFromFileHash
	var booksDir string
	if w.config != nil && w.config.BooksDir != "" {
		booksDirConfig := w.config.BooksDir
		// Проверяем, является ли путь абсолютным
		if filepath.IsAbs(booksDirConfig) {
			booksDir = booksDirConfig
		} else {
			// BooksDir относительный, пытаемся сделать его абсолютным через rootPath
			if w.rootPath != "" {
				booksDir = filepath.Join(w.rootPath, booksDirConfig)
			} else {
				// rootPath не задан, используем относительный путь как есть
				booksDir = booksDirConfig
			}
		}
	} else if w.rootPath != "" {
		booksDir = filepath.Join(w.rootPath, "books")
	} else {
		booksDir = "./books"
	}

	// Создаем каталог если он не существует
	if err := os.MkdirAll(booksDir, 0755); err != nil {
		return fmt.Errorf("ошибка создания каталога books: %w", err)
	}

	// Формируем путь к файлу в каталоге books
	destPath := filepath.Join(booksDir, originalName)

	// Если файл с таким именем уже существует, добавляем суффикс
	if _, err := os.Stat(destPath); err == nil {
		// Файл существует, добавляем суффикс
		ext := filepath.Ext(originalName)
		nameWithoutExt := strings.TrimSuffix(originalName, ext)
		for i := 1; ; i++ {
			newName := fmt.Sprintf("%s_%d%s", nameWithoutExt, i, ext)
			destPath = filepath.Join(booksDir, newName)
			if _, err := os.Stat(destPath); os.IsNotExist(err) {
				break
			}
		}
	}

	// Копируем файл из временного расположения в каталог books
	// Сначала открываем исходный файл
	srcFile, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("ошибка открытия исходного файла: %w", err)
	}
	defer srcFile.Close()

	// Создаем целевой файл
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("ошибка создания файла в каталоге books: %w", err)
	}
	defer destFile.Close()

	// Копируем содержимое
	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return fmt.Errorf("ошибка копирования файла: %w", err)
	}

	// Теперь обрабатываем файл как обычную книгу из каталога
	// Используем существующую функцию processBookFile из scanner пакета
	destInfo, err := os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("ошибка получения информации о файле: %w", err)
	}

	// Получаем абсолютный путь к файлу для сохранения в БД
	absDestPath, err := filepath.Abs(destPath)
	if err != nil {
		// Если не удалось получить абсолютный путь, используем destPath как есть
		absDestPath = destPath
		if cfg.Debug {
			log.Printf("Warning: Could not get absolute path for %s: %v", destPath, err)
		}
	}

	// Сохраняем абсолютный путь в контексте scanner перед обработкой
	// Это нужно, чтобы scanner мог сохранить правильный путь в БД
	err = scanner.ProcessBookFile(absDestPath, destInfo)
	if err != nil {
		// Если обработка не удалась, удаляем файл
		os.Remove(destPath)
		return fmt.Errorf("ошибка обработки файла: %w", err)
	}

	// IPFS загрузка теперь происходит внутри scanner.ProcessBookFile, как и было ранее
	// Нам не нужно делать это отдельно, так как scanner уже имеет доступ к конфигурации

	return nil
}
