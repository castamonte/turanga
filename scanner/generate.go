// scanner/generate.go
package scanner

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"turanga/config"
)

// GenerateMissingCovers создает недостающие обложки для всех книг в БД
func GenerateMissingCovers() error {
	cfg := config.GetConfig()
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}
	// ВСЕГДА определяем каталог covers относительно rootPath (каталога запуска)
	// игнорируя books_dir для веб-совместимости
	var coversDir string
	if rootPath != "" {
		coversDir = filepath.Join(rootPath, "covers")
	} else {
		coversDir = "./covers"
	}

	// Проверяем существование каталога covers
	if _, err := os.Stat(coversDir); os.IsNotExist(err) {
		// Если каталога нет, создаем его
		if err := os.MkdirAll(coversDir, 0755); err != nil {
			return fmt.Errorf("ошибка создания каталога covers: %w", err)
		}
		if cfg.Debug {
			log.Printf("Каталог covers создан: %s", coversDir)
		}
	} else if err != nil {
		return fmt.Errorf("ошибка проверки каталога covers: %w", err)
	}

	if cfg.Debug {
		log.Println("Начинаю генерацию недостающих обложек...")
	}

	// Получаем все книги из БД, у которых есть file_hash (не NULL и не пустая строка)
	// file_hash необходим для формирования имени файла обложки и её поиска
	rows, err := db.Query(`
		SELECT id, file_url, file_type, file_hash 
		FROM books 
		WHERE file_hash IS NOT NULL AND file_hash != '' AND file_url IS NOT NULL AND file_url != '' AND file_type IS NOT NULL AND file_type != ''
	`)
	if err != nil {
		return fmt.Errorf("ошибка получения списка книг из БД: %w", err)
	}
	defer rows.Close()

	processedCount := 0
	generatedCount := 0
	skippedNoHashCount := 0
	skippedNoFileCount := 0
	skippedExistsCount := 0
	errorCount := 0

	for rows.Next() {
		processedCount++
		var bookID int
		var fileURL, fileType, fileHash sql.NullString

		err := rows.Scan(&bookID, &fileURL, &fileType, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги (ID: %d): %v", bookID, err)
			}
			errorCount++
			continue
		}

		// Проверяем, есть ли file_hash
		if !fileHash.Valid || fileHash.String == "" {
			if cfg.Debug {
				log.Printf("Пропускаю книгу ID %d: отсутствует file_hash", bookID)
			}
			skippedNoHashCount++
			continue
		}

		// Проверяем, есть ли file_url
		if !fileURL.Valid || fileURL.String == "" {
			if cfg.Debug {
				log.Printf("Пропускаю книгу ID %d: отсутствует file_url", bookID)
			}
			skippedNoFileCount++
			continue
		}

		// Проверяем, есть ли file_type
		if !fileType.Valid || fileType.String == "" {
			if cfg.Debug {
				log.Printf("Пропускаю книгу ID %d: отсутствует file_type", bookID)
			}
			skippedNoFileCount++
			continue
		}

		// Формируем ожидаемое имя файла обложки
		coverFileName := fileHash.String + ".jpg" // ExtractCover всегда сохраняет как .jpg
		coverFilePath := filepath.Join(coversDir, coverFileName)

		// Проверяем, существует ли файл обложки
		if _, err := os.Stat(coverFilePath); err == nil {
			// Файл обложки уже существует
			if cfg.Debug {
				log.Printf("Обложка для книги ID %d (хеш: %s) уже существует: %s", bookID, fileHash.String, coverFilePath)
			}
			skippedExistsCount++
			continue
		} else if !os.IsNotExist(err) {
			// Другая ошибка при проверке существования файла
			if cfg.Debug {
				log.Printf("Ошибка проверки существования обложки %s для книги ID %d: %v", coverFilePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Файл обложки отсутствует, пытаемся его извлечь
		// ИСПОЛЬЗУЕМ АБСОЛЮТНЫЙ ПУТЬ ИЗ БД НАПРЯМУЮ
		filePath := fileURL.String

		// Проверяем существование исходного файла книги
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Исходный файл книги не найден, пропускаю обложку для книги ID %d: %s", bookID, filePath)
			}
			skippedNoFileCount++
			continue
		} else if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки исходного файла книги %s для книги ID %d: %v", filePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Вызываем ExtractCover из cover.go
		// ExtractCover сама проверит, существует ли обложка, и если нет - попытается извлечь
		coverURL, err := ExtractCover(filePath, fileType.String, bookID, fileHash.String)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка извлечения обложки для книги ID %d (файл: %s): %v", bookID, filePath, err)
			}
			errorCount++
			continue
		}

		if coverURL != "" {
			if cfg.Debug {
				log.Printf("Обложка успешно сгенерирована для книги ID %d: %s", bookID, coverURL)
			}
			generatedCount++
		} else {
			// ExtractCover вернула пустую строку, это означает, что обложка не была найдена/извлечена
			// Это не ошибка, просто нет обложки в файле книги
			if cfg.Debug {
				log.Printf("Обложка не найдена в файле книги ID %d: %s", bookID, filePath)
			}
		}
	}

	// Проверяем ошибки после итерации
	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}

	log.Printf("Генерация недостающих обложек завершена.")
	log.Printf("  Обработано записей: %d", processedCount)
	log.Printf("  Сгенерировано обложек: %d", generatedCount)
	log.Printf("  Пропущено (обложка уже существует): %d", skippedExistsCount)
	log.Printf("  Пропущено (нет file_hash): %d", skippedNoHashCount)
	log.Printf("  Пропущено (нет файла или file_url): %d", skippedNoFileCount)
	log.Printf("  Ошибок: %d", errorCount)

	return nil
}

// GenerateMissingAnnotations создает недостающие файлы аннотаций для всех книг в БД
func GenerateMissingAnnotations() error {
	cfg := config.GetConfig()
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}
	// ВСЕГДА определяем каталог notes относительно rootPath (каталога запуска)
	// игнорируя books_dir для веб-совместимости
	var notesDir string
	if rootPath != "" {
		notesDir = filepath.Join(rootPath, "notes")
	} else {
		notesDir = "./notes"
	}

	// Проверяем существование каталога notes
	if _, err := os.Stat(notesDir); os.IsNotExist(err) {
		// Если каталога нет, создаем его
		if err := os.MkdirAll(notesDir, 0755); err != nil {
			return fmt.Errorf("ошибка создания каталога notes: %w", err)
		}
		log.Printf("Каталог notes создан: %s", notesDir)
	} else if err != nil {
		return fmt.Errorf("ошибка проверки каталога notes: %w", err)
	}

	if cfg.Debug {
		log.Println("Начинаю генерацию недостающих аннотаций...")
	}

	// Получаем все книги из БД, у которых есть file_hash и file_url
	// file_hash необходим для формирования имени файла аннотации и её поиска
	// file_url необходим для доступа к исходному файлу книги
	rows, err := db.Query(`
		SELECT id, file_url, file_type, file_hash 
		FROM books 
		WHERE file_hash IS NOT NULL AND file_hash != '' AND file_url IS NOT NULL AND file_url != ''
	`)
	if err != nil {
		return fmt.Errorf("ошибка получения списка книг из БД: %w", err)
	}
	defer rows.Close()

	processedCount := 0
	generatedCount := 0
	skippedNoHashCount := 0
	skippedNoFileCount := 0
	skippedExistsCount := 0 // Новые счетчики
	errorCount := 0

	for rows.Next() {
		processedCount++
		var bookID int
		var fileURL, fileType, fileHash sql.NullString

		err := rows.Scan(&bookID, &fileURL, &fileType, &fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки книги (ID: %d): %v", bookID, err)
			}
			errorCount++
			continue
		}

		// Проверяем, есть ли file_hash
		if !fileHash.Valid || fileHash.String == "" {
			skippedNoHashCount++
			continue
		}

		// Проверяем, есть ли file_url
		if !fileURL.Valid || fileURL.String == "" {
			skippedNoFileCount++
			continue
		}

		// Проверяем, есть ли file_type (желательно, но не критично для аннотаций)
		if !fileType.Valid || fileType.String == "" {
			// Можно продолжить и без типа, пытаясь определить по расширению позже
			// Но для простоты пропустим. Можно убрать это условие.
			// skippedNoFileCount++
			// continue
			// Пока оставим, логика extractMetadata может справиться.
		}

		// Формируем ожидаемое имя файла аннотации
		noteFileName := fileHash.String + ".txt"
		noteFilePath := filepath.Join(notesDir, noteFileName)

		// Проверяем, существует ли файл аннотации
		if _, err := os.Stat(noteFilePath); err == nil {
			// Файл аннотации уже существует
			skippedExistsCount++
			continue
		} else if !os.IsNotExist(err) {
			// Другая ошибка при проверке существования файла
			if cfg.Debug {
				log.Printf("Ошибка проверки существования аннотации %s для книги ID %d: %v", noteFilePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Файл аннотации отсутствует, пытаемся его извлечь
		// ИСПОЛЬЗУЕМ АБСОЛЮТНЫЙ ПУТЬ ИЗ БД НАПРЯМУЙ
		filePath := fileURL.String

		// Проверяем существование исходного файла книги
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Исходный файл книги не найден, пропускаю аннотацию для книги ID %d: %s", bookID, filePath)
			}
			skippedNoFileCount++
			continue
		} else if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка проверки исходного файла книги %s для книги ID %d: %v", filePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Получаем информацию о файле для extractMetadata
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка получения информации о файле %s для книги ID %d: %v", filePath, bookID, err)
			}
			errorCount++
			continue
		}

		// Извлекаем метаданные, включая аннотацию, используя существующую логику
		// extractMetadata возвращает много полей, но нам нужна только аннотация (и fileHash для saveAnnotationToFile)
		// Поля: fileType, author, title, annotation, isbn, year, publisher, series, series_number, err
		_, _, _, annotation, _, _, _, _, _, err := extractMetadata(filePath, fileInfo)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка извлечения метаданных (включая аннотацию) для книги ID %d (файл: %s): %v", bookID, filePath, err)
			}
			// Это не критично, аннотация может отсутствовать
			// Просто сохраним пустую аннотацию или пропустим
			annotation = "" // Будет создан пустой файл или файл не будет создан
		}

		// Сохраняем аннотацию в файл, используя существующую функцию
		// saveAnnotationToFile(bookID int, annotation string, fileHash string)
		// bookID используется только для логов внутри функции, можно передать 0 или реальный ID
		err = saveAnnotationToFile(bookID, annotation, fileHash.String)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сохранения аннотации для книги ID %d (хеш: %s): %v", bookID, fileHash.String, err)
			}
			errorCount++
			continue
		}

		// Проверим, была ли аннотация не пустой и файл создан
		if annotation != "" {
			if _, err := os.Stat(noteFilePath); err == nil {
				if cfg.Debug {
					log.Printf("Аннотация успешно сгенерирована для книги ID %d: %s", bookID, noteFileName)
				}
				generatedCount++
			} else {
				// saveAnnotationToFile не создает файл, если аннотация пустая
				if cfg.Debug {
					log.Printf("Аннотация для книги ID %d оказалась пустой, файл не создан", bookID)
				}
				// Это нормально, не считаем за ошибку
				// Можем залогировать, если аннотация была, но файл не создался по другой причине
			}
		} else {
			if cfg.Debug {
				log.Printf("Аннотация для книги ID %d отсутствует в файле", bookID)
			}
			// Это нормально
		}
	}

	// Проверяем ошибки после итерации
	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}

	log.Printf("Генерация недостающих аннотаций завершена.")
	log.Printf("  Обработано записей: %d", processedCount)
	log.Printf("  Сгенерировано аннотаций (файлов создано): %d", generatedCount)
	log.Printf("  Пропущено (аннотация уже существует): %d", skippedExistsCount)
	log.Printf("  Пропущено (нет file_hash): %d", skippedNoHashCount)
	log.Printf("  Пропущено (нет файла или file_url): %d", skippedNoFileCount)
	log.Printf("  Ошибок: %d", errorCount)

	return nil
}

// RenameBooksAccordingToConfig переименовывает книги согласно настройкам конфигурации
func RenameBooksAccordingToConfig() error {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Println("Начинаем переименование книг по конфигурации")
	}

	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}

	if cfg == nil {
		log.Println("Конфигурация не установлена, пропускаю переименование")
		return nil
	}

	renameMode := cfg.GetRenameBook()
	if renameMode == "no" {
		log.Println("Режим переименования 'no', пропускаю переименование")
		return nil
	}

	// Определяем каталог books
	booksDir := cfg.GetBooksDirAbs(rootPath)
	if cfg.Debug {
		log.Printf("Каталог books: %s", booksDir)
	}

	// Сначала получаем все книги в слайс, чтобы закрыть rows сразу
	type bookInfo struct {
		id       int
		fileURL  string
		title    string
		fileType string
		fileHash string
		authors  string
	}

	var books []bookInfo

	// Получаем все книги из БД с авторами
	rows, err := db.Query(`
        SELECT b.id, b.file_url, b.title, b.file_type, b.file_hash, 
               GROUP_CONCAT(a.full_name, ', ') as authors
        FROM books b
        LEFT JOIN book_authors ba ON b.id = ba.book_id
        LEFT JOIN authors a ON ba.author_id = a.id
        WHERE b.file_url IS NOT NULL AND b.file_url != ''
        GROUP BY b.id, b.file_url, b.title, b.file_type, b.file_hash
    `)
	if err != nil {
		return fmt.Errorf("ошибка получения списка книг: %w", err)
	}

	for rows.Next() {
		var id int
		var fileURL, title, fileType, fileHash, authors sql.NullString

		if err := rows.Scan(&id, &fileURL, &title, &fileType, &fileHash, &authors); err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки: %v", err)
			}
			continue
		}

		// Проверяем обязательные поля
		if !fileURL.Valid || fileURL.String == "" {
			continue
		}

		books = append(books, bookInfo{
			id:       id,
			fileURL:  fileURL.String,
			title:    getValueOrDefault(title),
			fileType: getValueOrDefault(fileType),
			fileHash: getValueOrDefault(fileHash),
			authors:  getValueOrDefault(authors),
		})
	}

	if err = rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("ошибка итерации по результатам: %w", err)
	}
	rows.Close()

	// Теперь обрабатываем книги
	renamedCount := 0
	errorCount := 0
	skippedCount := 0

	for _, book := range books {
		// Для полных путей используем fileURL напрямую
		filePath := book.fileURL

		// Проверяем, находится ли файл в каталоге books
		// Преобразуем пути для корректного сравнения
		absFilePath, err := filepath.Abs(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка получения абсолютного пути для %s: %v", filePath, err)
			}
			skippedCount++
			continue
		}

		absBooksDir, err := filepath.Abs(booksDir)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка получения абсолютного пути для каталога books %s: %v", booksDir, err)
			}
			skippedCount++
			continue
		}

		// Проверяем, находится ли файл в каталоге books или его подкаталогах
		relPath, err := filepath.Rel(absBooksDir, absFilePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Файл %s не находится в каталоге books, пропускаю: %v", filePath, err)
			}
			skippedCount++
			continue
		}

		// Проверяем, что относительный путь не начинается с ".." (это означало бы, что файл вне каталога books)
		if strings.HasPrefix(relPath, "..") {
			if cfg.Debug {
				log.Printf("Файл %s не находится в каталоге books, пропускаю", filePath)
			}
			skippedCount++
			continue
		}

		// Проверяем, существует ли файл
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if cfg.Debug {
				log.Printf("Файл не найден: %s (ID: %d)", filePath, book.id)
			}
			skippedCount++
			continue
		}

		// Проверяем, нужно ли переименовывать (только для режима autit)
		if renameMode == "autit" {
			// Генерируем ожидаемое имя файла
			expectedPath := generateExpectedFileName(filePath, book.authors, book.title, book.fileType, book.fileHash)

			// Если имя уже правильное, пропускаем
			if expectedPath == filePath {
				skippedCount++
				continue
			}
		}

		// Переименовываем файл согласно конфигурации
		newPath, err := renameBookFile(filePath, book.authors, book.title, book.fileType, book.fileHash)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка переименования файла %s: %v", filePath, err)
			}
			errorCount++
			continue
		}

		// Если путь изменился, обновляем БД
		if newPath != filePath {
			// Обновляем БД с новым абсолютным путем
			_, err = db.Exec("UPDATE books SET file_url = ? WHERE id = ?", newPath, book.id)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка обновления пути в БД для книги ID %d: %v", book.id, err)
				}
				errorCount++
				continue
			}
			renamedCount++
			if cfg.Debug {
				log.Printf("Книга ID %d переименована: %s -> %s", book.id, filePath, newPath)
			}
		} else {
			skippedCount++
		}
	}

	log.Printf("Переименование завершено. Переименовано: %d, Пропущено: %d, Ошибок: %d",
		renamedCount, skippedCount, errorCount)
	return nil
}

// renameBookFile переименовывает файл книги в соответствии с настройками конфигурации
func renameBookFile(originalPath, authorName, title, fileType, fileHash string) (newPath string, err error) {
	if cfg == nil {
		// Если конфигурация не установлена, не переименовываем
		fmt.Println("Конфигурация не установлена, пропускаю переименование")
		return originalPath, nil
	}

	renameMode := cfg.GetRenameBook()

	// Если "no", не переименовываем
	if renameMode == "no" {
		return originalPath, nil
	}

	// Исправленное определение расширения файла
	// Учитываем специальные случаи, такие как .fb2.zip
	var ext string
	lowerOriginalPath := strings.ToLower(originalPath)
	if strings.HasSuffix(lowerOriginalPath, ".fb2.zip") {
		ext = ".fb2.zip"
	} else {
		// Для остальных файлов используем стандартное расширение
		ext = filepath.Ext(originalPath)
	}

	// Получаем директорию оригинального файла
	// ЭТО КРИТИЧЕСКИ ВАЖНО: сохраняем директорию
	dir := filepath.Dir(originalPath)

	var newName string
	switch renameMode {
	case "autit":
		// Формат: Имя_Автора-Название_книги.ext
		var sanitizedAuthor string

		// Проверяем, есть ли несколько авторов (разделены запятыми или точкой с запятой)
		if strings.Contains(authorName, ",") || strings.Contains(authorName, ";") {
			// Несколько авторов - используем "Коллектив_авторов"
			sanitizedAuthor = "Коллектив_авторов"
		} else {
			// Один автор - используем его имя
			sanitizedAuthor = sanitizeFilename(filepath.Base(authorName))
		}

		// Очищаем только имя автора (если это не "Коллектив_авторов")
		if sanitizedAuthor != "Коллектив_авторов" {
			sanitizedAuthor = strings.ReplaceAll(sanitizedAuthor, " ", "_")
		}

		// Очищаем название книги
		sanitizedTitle := sanitizeFilename(filepath.Base(title))
		sanitizedTitle = strings.ReplaceAll(sanitizedTitle, " ", "_")

		// Формируем имя файла
		newName = fmt.Sprintf("%s-%s%s", sanitizedAuthor, sanitizedTitle, ext)
	case "hash":
		// Формат: xxhash.ext
		newName = fmt.Sprintf("%s%s", fileHash, ext)
	default:
		// На случай, если валидация не сработала
		fmt.Printf("Неизвестный режим переименования '%s', пропускаю\n", renameMode)
		return originalPath, nil
	}

	// Формируем новый путь, объединяя оригинальную директорию с новым именем
	// ЭТО КРИТИЧЕСКИ ВАЖНО: используем оригинальную директорию
	newPath = filepath.Join(dir, newName)

	// Очищаем только базовое имя нового файла, не трогая путь к директории
	// Это дополнительная мера предосторожности
	newPath = sanitizeFilename(newPath)

	// Проверяем, отличается ли новый путь от старого
	absOriginal, _ := filepath.Abs(originalPath)
	absNew, _ := filepath.Abs(newPath)
	if absOriginal == absNew {
		return originalPath, nil
	}

	// Проверяем, существует ли файл с таким именем
	if _, err := os.Stat(newPath); err == nil {
		// Файл с таким именем уже существует
		fmt.Printf("Файл с именем %s уже существует, проверяю хеши...\n", newName)

		// Вычисляем хеш существующего файла
		existingFileHash, hashErr := calculateFileHash(newPath)
		if hashErr != nil {
			fmt.Printf("Ошибка вычисления хеша существующего файла %s: %v\n", newPath, hashErr)
			// Не переименовываем, возвращаем оригинальный путь
			return originalPath, nil
		}

		// Вычисляем хеш оригинального файла
		originalFileHash, hashErr := calculateFileHash(originalPath)
		if hashErr != nil {
			fmt.Printf("Ошибка вычисления хеша оригинального файла %s: %v\n", originalPath, hashErr)
			// Не переименовываем, возвращаем оригинальный путь
			return originalPath, nil
		}

		// Сравниваем хеши
		if existingFileHash == originalFileHash {
			// Хеши совпадают - файлы идентичны
			// Удаляем оригинальный файл
			fmt.Printf("Хеши совпадают, удаляю оригинальный файл: %s\n", originalPath)
			deleteErr := os.Remove(originalPath)
			if deleteErr != nil {
				fmt.Printf("Ошибка удаления оригинального файла %s: %v\n", originalPath, deleteErr)
				// Не возвращаем ошибку, просто не переименовываем
				return originalPath, nil
			}
			// Возвращаем путь к существующему файлу (новому имени)
			return newPath, nil
		} else {
			// Хеши не совпадают - файлы разные
			fmt.Printf("Файл %s существует, но хеши не совпадают. Оригинальный файл: %s (хеш: %s), Существующий файл: %s (хеш: %s). Пропускаю.\n",
				newName, originalPath, originalFileHash, newPath, existingFileHash)
			// Не переименовываем, возвращаем оригинальный путь
			return originalPath, nil
		}
	} else if !os.IsNotExist(err) {
		// Другая ошибка при проверке существования файла
		return originalPath, fmt.Errorf("ошибка проверки существования файла %s: %w", newPath, err)
	}

	// Переименовываем файл
	err = os.Rename(originalPath, newPath)
	if err != nil {
		return originalPath, fmt.Errorf("ошибка переименования файла %s в %s: %w", originalPath, newPath, err)
	}

	fmt.Printf("Файл переименован: %s -> %s\n", originalPath, newPath)
	return newPath, nil
}

// generateExpectedFileName генерирует ожидаемое имя файла без фактического переименования
func generateExpectedFileName(originalPath, authorName, title, fileType, fileHash string) string {
	if cfg == nil {
		return originalPath
	}

	renameMode := cfg.GetRenameBook()
	if renameMode == "no" {
		return originalPath
	}

	// Исправленное определение расширения файла
	var ext string
	lowerOriginalPath := strings.ToLower(originalPath)
	if strings.HasSuffix(lowerOriginalPath, ".fb2.zip") {
		ext = ".fb2.zip"
	} else {
		ext = filepath.Ext(originalPath)
	}

	dir := filepath.Dir(originalPath)
	var newName string

	switch renameMode {
	case "autit":
		var sanitizedAuthor string

		// Проверяем, есть ли несколько авторов
		if strings.Contains(authorName, ",") || strings.Contains(authorName, ";") {
			sanitizedAuthor = "Коллектив_авторов"
		} else if authorName != "" {
			sanitizedAuthor = sanitizeFilename(filepath.Base(authorName))
			sanitizedAuthor = strings.ReplaceAll(sanitizedAuthor, " ", "_")
		} else {
			sanitizedAuthor = "Неизвестный_автор"
		}

		sanitizedTitle := ""
		if title != "" {
			sanitizedTitle = sanitizeFilename(filepath.Base(title))
			sanitizedTitle = strings.ReplaceAll(sanitizedTitle, " ", "_")
		} else {
			// Используем имя файла если нет названия
			baseName := filepath.Base(originalPath)
			ext := filepath.Ext(baseName)
			sanitizedTitle = sanitizeFilename(strings.TrimSuffix(baseName, ext))
		}

		newName = fmt.Sprintf("%s-%s%s", sanitizedAuthor, sanitizedTitle, ext)
	case "hash":
		newName = fmt.Sprintf("%s%s", fileHash, ext)
	default:
		return originalPath
	}

	newPath := filepath.Join(dir, newName)
	newPath = sanitizeFilename(newPath)

	return newPath
}
