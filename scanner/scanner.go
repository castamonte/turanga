// scanner/scanner.go
package scanner

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"turanga/config"

	xxhash "github.com/cespare/xxhash/v2"

	//shell "github.com/ipfs/go-ipfs-api"
	ipfsapi "github.com/ipfs/go-ipfs-api"
)

// Author представляет автора книги
type Author struct {
	LastName string // Для сортировки/поиска
	FullName string // Для отображения
}

var db *sql.DB
var cfg *config.Config

// SetDB устанавливает соединение с базой данных
func SetDB(database *sql.DB) {
	db = database
}

// SetConfig устанавливает конфигурацию приложения
func SetConfig(config *config.Config) {
	cfg = config
}

var rootPath string

// SetRootPath устанавливает корневую директорию приложения
func SetRootPath(path string) {
	rootPath = path
}

// sanitizeFilename очищает строку от недопустимых символов для имен файлов
// Исправленная версия с акцентом на корректную обработку путей
func sanitizeFilename(s string) string {
	if s == "" {
		return "unnamed"
	}

	// Разделяем путь на директорию и имя файла
	dir := filepath.Dir(s)
	baseName := filepath.Base(s)

	if baseName == "." || baseName == ".." {
		baseName = "unnamed"
	}

	// Очищаем только имя файла, не трогая структуру каталогов
	// Заменяем недопустимые символы на подчеркивание
	// Согласно спецификации FAT32/NTFS/Unix
	invalidChars := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
	for _, char := range invalidChars {
		baseName = strings.ReplaceAll(baseName, char, "_")
	}

	// Заменяем управляющие символы (0-31)
	for i := 0; i < 32; i++ {
		baseName = strings.ReplaceAll(baseName, string(rune(i)), "_")
	}

	// Убираем точки и пробелы в конце имени файла
	baseName = strings.TrimRight(baseName, ". ")

	// Если после очистки имя пустое
	if baseName == "" {
		baseName = "unnamed"
	}

	// Ограничиваем длину имени файла (без пути)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	// Ограничение длины имени файла без расширения
	if len(nameWithoutExt) > 150 {
		nameWithoutExt = nameWithoutExt[:150]
	}

	// Также ограничиваем общую длину имени файла
	if len(nameWithoutExt+ext) > 200 {
		nameWithoutExt = nameWithoutExt[:200-len(ext)]
		if len(nameWithoutExt) <= 0 {
			nameWithoutExt = "unnamed"
		}
	}

	// Собираем очищенное имя файла
	cleanedBaseName := nameWithoutExt + ext

	// Если имя файла стало "пустым" или недопустимым, используем дефолтное
	if cleanedBaseName == "" || cleanedBaseName == "." || cleanedBaseName == ".." {
		cleanedBaseName = "unnamed" + ext
	}

	// Собираем обратно путь
	result := filepath.Join(dir, cleanedBaseName)

	// Финальная проверка на пустоту
	if result == "" {
		result = filepath.Join(".", "unnamed"+ext) // По крайней мере в текущей директории
	}

	return result
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
		fmt.Printf("Файл с именем %s уже существует, пропускаю переименование %s\n", newName, originalPath)
		return originalPath, nil
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

// calculateFileHash вычисляет xxHash3 для файла
func calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("не удалось открыть файл для хеширования %s: %w", filePath, err)
	}
	defer file.Close()
	h := xxhash.New()
	// Копируем содержимое файла в хешер
	_, err = io.Copy(h, file)
	if err != nil {
		return "", fmt.Errorf("ошибка при чтении файла для хеширования %s: %w", filePath, err)
	}
	// Возвращаем хеш в виде строки
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// ScanBooksDirectory сканирует каталог с книгами и добавляет в БД файлы, которых там нет
func ScanBooksDirectory() error {
	booksDir := "./books"

	// Используем ту же логику, что и в ScanForNewFiles
	if cfg != nil && cfg.BooksDir != "" {
		booksDirConfig := cfg.BooksDir
		// Проверяем, является ли путь абсолютным
		if filepath.IsAbs(booksDirConfig) {
			booksDir = booksDirConfig
		} else {
			// BooksDir относительный, пытаемся сделать его абсолютным через rootPath
			if rootPath != "" {
				booksDir = filepath.Join(rootPath, booksDirConfig)
			}
		}
	} else if rootPath != "" {
		booksDir = filepath.Join(rootPath, "books")
	}

	// Проверяем существование каталога
	if _, err := os.Stat(booksDir); os.IsNotExist(err) {
		fmt.Printf("Каталог books не найден: %s, создаю...\n", booksDir)
		err = os.MkdirAll(booksDir, 0755)
		if err != nil {
			return err
		}
		return nil
	}

	fmt.Printf("Сканирую каталог books: %s\n", booksDir)

	// Получаем все хеши файлов из БД для проверки дубликатов
	rows, err := db.Query("SELECT file_hash FROM books WHERE file_hash IS NOT NULL AND file_hash != ''")
	if err != nil {
		return fmt.Errorf("ошибка получения хешей из БД: %w", err)
	}
	defer rows.Close()

	// Создаем множество существующих хешей
	existingHashes := make(map[string]bool)
	for rows.Next() {
		var fileHash string
		if err := rows.Scan(&fileHash); err == nil {
			existingHashes[fileHash] = true
		}
	}

	// Счетчики
	addedCount := 0
	skippedCount := 0
	errorCount := 0

	err = filepath.Walk(booksDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Проверяем расширения файлов
		ext := strings.ToLower(filepath.Ext(path))
		validExts := []string{".fb2", ".epub", ".pdf", ".djvu"}
		isValidExt := false
		for _, validExt := range validExts {
			if ext == validExt {
				isValidExt = true
				break
			}
		}

		if ext == ".zip" {
			// Проверяем, может это fb2.zip
			baseName := strings.TrimSuffix(info.Name(), ext)
			if strings.HasSuffix(strings.ToLower(baseName), ".fb2") {
				isValidExt = true
			}
		}

		if isValidExt {
			// Вычисляем хеш файла для проверки дубликатов
			fileHash, hashErr := calculateFileHash(path)
			if hashErr != nil {
				fmt.Printf("Ошибка вычисления хеша для файла %s: %v\n", path, hashErr)
				errorCount++
				return nil
			}

			// Проверяем, существует ли файл с таким хешем в БД
			if existingHashes[fileHash] {
				// Файл уже есть в БД
				skippedCount++
				fmt.Printf("Файл уже в БД (пропущен): %s\n", path)
				return nil
			}

			// Файл новый, обрабатываем его
			fmt.Printf("Обрабатываю новый файл: %s\n", path)
			processErr := processBookFile(path, info)
			if processErr != nil {
				fmt.Printf("Ошибка обработки файла %s: %v\n", path, processErr)
				errorCount++
			} else {
				addedCount++
				fmt.Printf("Файл добавлен в БД: %s\n", path)

				// Генерируем обложку и аннотацию для нового файла
				// Получаем ID новой книги из БД
				var bookID int
				var fileType sql.NullString
				err = db.QueryRow("SELECT id, file_type FROM books WHERE file_hash = ?", fileHash).Scan(&bookID, &fileType)
				if err != nil {
					fmt.Printf("Ошибка получения ID новой книги (хеш: %s): %v\n", fileHash, err)
				} else {
					// Генерируем обложку
					if fileType.Valid && fileType.String != "" {
						coverURL, coverErr := ExtractCover(path, fileType.String, bookID, fileHash)
						if coverErr != nil {
							fmt.Printf("Предупреждение: ошибка извлечения обложки для новой книги ID %d: %v\n", bookID, coverErr)
						} else if coverURL != "" {
							fmt.Printf("Обложка извлечена: %s\n", coverURL)
						}
					}

					// Генерируем аннотацию
					_, _, _, annotation, _, _, _, _, _, metaErr := extractMetadata(path, info)
					if metaErr != nil {
						if cfg.Debug {
							log.Printf("Предупреждение: ошибка извлечения метаданных для новой книги ID %d: %v", bookID, metaErr)
						}
						annotation = ""
					} else {
						if cfg.Debug {
							log.Printf("Извлечена аннотация длиной: %d символов", len(annotation))
						}
					}
					saveErr := saveAnnotationToFile(bookID, annotation, fileHash)
					if saveErr != nil {
						if cfg.Debug {
							log.Printf("Предупреждение: ошибка сохранения аннотации для новой книги ID %d: %v", bookID, saveErr)
						}
					} else if annotation != "" {
						if cfg.Debug {
							log.Printf("Аннотация сохранена для книги ID %d", bookID)
						}
					}
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("ошибка сканирования каталога: %w", err)
	}

	fmt.Printf("Сканирование завершено. Добавлено: %d, Пропущено: %d, Ошибок: %d\n", addedCount, skippedCount, errorCount)
	return nil
}

// processBookFile обрабатывает один файл книги
// Всегда добавляет файл в IPFS и сохраняет CID.
func processBookFile(filePath string, info os.FileInfo) error {
	// Проверка хеша файла
	fileHash, err := calculateFileHash(filePath)
	if err != nil {
		fmt.Printf("Предупреждение: Не удалось вычислить хеш для файла %s: %v. Пропускаю.\n", filePath, err)
		return nil
	}

	// Проверяем, существует ли файл с таким хешем в базе данных
	// Теперь хеш и ipfs_cid хранятся прямо в таблице books
	var existingBookID int
	var existingFileURL, existingIPFSCID sql.NullString
	err = db.QueryRow("SELECT id, file_url, ipfs_cid FROM books WHERE file_hash = ?", fileHash).Scan(&existingBookID, &existingFileURL, &existingIPFSCID)
	if err == nil {
		// Файл с таким хешем уже существует в БД
		cidInfo := ""
		if existingIPFSCID.Valid && existingIPFSCID.String != "" {
			cidInfo = fmt.Sprintf(", IPFS CID: %s", existingIPFSCID.String)
		}
		fmt.Printf("Файл %s (хеш: %s%s) уже существует в БД, ID %d (URL: %s). Пропускаю.\n", filePath, fileHash, cidInfo, existingBookID, existingFileURL.String)
		return nil // Прекращаем обработку этого файла
	} else if err != sql.ErrNoRows {
		// Произошла другая ошибка при запросе к БД
		return fmt.Errorf("ошибка проверки существования файла по хешу %s: %w", fileHash, err)
	}

	// Сохраняем оригинальный путь для потенциального переименования
	originalFilePath := filePath

	// Определяем тип файла и извлекаем метаданные
	fileType, authorName, title, annotation, isbn, year, publisher, series, seriesNumber, err := extractMetadata(filePath, info)
	if err != nil {
		fmt.Printf("Не удалось определить тип файла для %s: %v\n", filePath, err)
		return nil
	}

	// Если не удалось извлечь метаданные или нет названия, используем имя файла
	if title == "" {
		authorName, title = extractInfoFromFilename(info.Name())
		fmt.Printf("Использую данные из имени файла для %s: Автор='%s', Название='%s'\n", filePath, authorName, title)
	}

	// Проверка, что у нас есть хотя бы название
	if title == "" {
		fmt.Printf("Не удалось определить название для %s\n", filePath)
		return nil // Не ошибка, просто пропускаем файл
	}

	// Переименовываем файл, если это указано в конфигурации
	// Делаем это до вычисления относительного пути, чтобы путь был актуальным
	filePath, err = renameBookFile(originalFilePath, authorName, title, fileType, fileHash)
	if err != nil {
		fmt.Printf("Предупреждение: ошибка переименования файла %s: %v\n", originalFilePath, err)
		// Продолжаем обработку с оригинальным путем
		filePath = originalFilePath
	}

	// Получаем абсолютный путь к файлу для хранения в БД
	absoluteFilePath, err := filepath.Abs(filePath)
	if err != nil {
		// Если не удалось получить абсолютный путь, используем filePath как есть
		absoluteFilePath = filePath
		fmt.Printf("Предупреждение: Не удалось получить абсолютный путь для %s: %v\n", filePath, err)
	}

	// Извлекаем относительный путь (уже с новым именем файла, если переименование произошло)
	var relPath string
	if rootPath != "" {
		// Если задан rootPath, формируем относительный путь от него
		relPath, err = filepath.Rel(rootPath, filePath)
		if err != nil {
			return fmt.Errorf("не удалось получить относительный путь от %s к %s: %w", rootPath, filePath, err)
		}
	} else {
		// Если rootPath не задан, используем старую логику (для обратной совместимости)
		relPath, err = filepath.Rel(".", filePath)
		if err != nil {
			return err
		}
	}

	// Подготавливаем дату публикации
	publishedAt := time.Now().UTC().Format("2006-01-02")
	if year != "" && len(year) == 4 {
		publishedAt = year + "-01-01"
	}
	// Получаем размер файла
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("ошибка получения информации о файле %s: %w", filePath, err)
	}
	fileSize := fileInfo.Size()

	// Создаем новую книгу сразу с информацией о файле
	result, err := db.Exec(
		`INSERT INTO books 
		(title, series, series_number, published_at, isbn, year, publisher, file_url, file_type, file_hash, file_size, over18, ipfs_cid) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		title, series, seriesNumber, publishedAt, isbn, year, publisher, absoluteFilePath, fileType, fileHash, fileSize, false, nil, // false = не 18+, ipfs_cid = nil -> NULL в БД
	)

	if err != nil {
		return fmt.Errorf("ошибка вставки новой книги '%s': %w", title, err)
	}

	lastInsertID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("ошибка получения ID новой книги '%s': %w", title, err)
	}
	bookID := int(lastInsertID)

	// Добавляем файл в IPFS после успешной вставки в БД
	ipfsCID := "" // Инициализируем переменную для CID

	// Получаем конфигурацию (предполагается, что она доступна глобально)
	if cfg != nil {
		ipfsShell, shellErr := cfg.GetIPFSShell()
		if shellErr == nil { // Проверяем только на nil err, так как IPFS всегда включен
			// Открываем файл для чтения
			fileForIPFS, openErr := os.Open(filePath)
			if openErr != nil {
				fmt.Printf("Предупреждение: Не удалось открыть файл %s для добавления в IPFS: %v.\n", filePath, openErr)
			} else {
				// КРИТИЧЕСКОЕ ИЗМЕНЕНИЕ: Закрываем файл НЕМЕДЛЕННО после использования
				func() {
					defer fileForIPFS.Close() // Закрываем сразу после Add

					// Добавляем файл в IPFS с --nocopy и --pin
					cid, addErr := ipfsShell.Add(fileForIPFS, ipfsapi.Pin(true))
					if addErr != nil {
						// Не критично для основного процесса, логируем предупреждение
						fmt.Printf("Предупреждение: Не удалось добавить файл %s в IPFS: %v.\n", filePath, addErr)
						// Можно рассмотреть возврат ошибки, если IPFS критичен
						// return fmt.Errorf("критическая ошибка добавления в IPFS: %w", addErr)
					} else {
						ipfsCID = cid
						fmt.Printf("Файл %s успешно добавлен в IPFS с CID: %s\n", filePath, ipfsCID)
						// Обновляем запись в БД с полученным CID
						_, updateErr := db.Exec("UPDATE books SET ipfs_cid = ? WHERE id = ?", ipfsCID, bookID)
						if updateErr != nil {
							fmt.Printf("Предупреждение: Не удалось сохранить IPFS CID в БД для книги ID %d: %v.\n", bookID, updateErr)
							// Не возвращаем ошибку, чтобы не прерывать основной процесс
						}
					}
				}() // Немедленный вызов для закрытия файла
			}
		}
	} else {
		fmt.Println("Предупреждение: Конфигурация не установлена. Продолжаю без добавления в IPFS.")
	}

	// После успешной вставки книги сохраняем аннотацию в файл:
	err = saveAnnotationToFile(bookID, annotation, fileHash)
	if err != nil {
		fmt.Printf("Предупреждение: ошибка сохранения аннотации для книги %d: %v\n", bookID, err)
		// Не прерываем процесс из-за ошибки аннотации
	}

	log.Printf("Добавлена новая книга: %s (ID: %d)", title, bookID)
	log.Printf("  ISBN: %s, Год: %s, Издатель: %s, Серия: %s", isbn, year, publisher, series)
	log.Printf("  Файл: %s (%s), хеш: %s", relPath, fileType, fileHash)
	if ipfsCID != "" {
		log.Printf("  IPFS CID: %s", ipfsCID)
	}

	// Обрабатываем авторов
	err = upsertAuthorsAndLink(bookID, authorName)
	if err != nil {
		fmt.Printf("Предупреждение: ошибка обработки авторов для %s: %v\n", filePath, err)
		// Не прерываем процесс из-за ошибки авторов
	}

	// Извлекаем обложку
	err = extractAndSaveCover(filePath, fileType, bookID, fileHash)
	if err != nil {
		fmt.Printf("Предупреждение: не удалось извлечь обложку для %s: %v\n", filePath, err)
		// Не прерываем процесс из-за ошибки обложки
	}

	fmt.Printf("Успешно обработан файл: %s (Название: '%s', Автор: '%s', Хеш: %s)\n", filePath, title, authorName, fileHash)
	return nil
}

// extractAndSaveCover извлекает и сохраняет обложку книги
func extractAndSaveCover(filePath, fileType string, bookID int, fileHash string) error {
	// Вызываем ExtractCover, передавая fileHash
	coverURL, err := ExtractCover(filePath, fileType, bookID, fileHash)
	if err == nil && coverURL != "" {
		// Не сохраняем coverURL в БД, просто логируем
		fmt.Printf("Добавлена обложка для книги %d: %s\n", bookID, filepath.Base(coverURL))
	}
	return err
}

// extractMetadata извлекает метаданные из файла книги
func extractMetadata(filePath string, info os.FileInfo) (fileType, author, title, annotation, isbn, year, publisher, series, series_number string, err error) {
	cfg := config.GetConfig()

	ext := strings.ToLower(filepath.Ext(filePath))

	if cfg.Debug {
		log.Printf("Извлекаю метаданные из файла: %s (расширение: %s)", filePath, ext)
	}

	switch ext {
	case ".fb2":
		fileType = "fb2"
		author, title, annotation, isbn, year, publisher, series, series_number, err = ExtractFB2Metadata(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("FB2 ошибка: %v для %s", err, filePath)
			}
			err = nil
		} else {
			if cfg.Debug {
				log.Printf("FB2 успешно обработан: Автор='%s', Название='%s', Аннотация=%d символов",
					author, title, len(annotation))
			}
		}
	case ".epub":
		fileType = "epub"
		author, title, annotation, isbn, year, publisher, series, series_number, err = ExtractEPUBMetadata(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("EPUB ошибка: %v для %s", err, filePath)
			}
			err = nil
		} else {
			if cfg.Debug {
				log.Printf("EPUB успешно обработан: Автор='%s', Название='%s', Аннотация=%d символов",
					author, title, len(annotation))
			}
		}
	case ".pdf":
		fileType = "pdf"
		author, title, err = ExtractPDFMetadata(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("PDF ошибка: %v для %s", err, filePath)
			}
			err = nil
		} else {
			if cfg.Debug {
				log.Printf("PDF успешно обработан: Автор='%s', Название='%s'", author, title)
			}
		}
	case ".djvu":
		fileType = "djvu"
		author, title, err = ExtractDJVUMetadata(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("DJVU ошибка (метаданные): %v для %s", err, filePath)
			}
			err = nil
		} else {
			if cfg.Debug {
				log.Printf("DJVU успешно обработан: Автор='%s', Название='%s'", author, title)
			}
		}
	case ".zip":
		if strings.Contains(strings.ToLower(strings.TrimSuffix(info.Name(), ext)), ".fb2") {
			fileType = "fb2.zip"
			author, title, annotation, isbn, year, publisher, series, series_number, err = ExtractFB2ZipMetadata(filePath)
			if err != nil {
				if cfg.Debug {
					log.Printf("FB2.ZIP ошибка: %v для %s", err, filePath)
				}
				err = nil
			} else {
				if cfg.Debug {
					log.Printf("FB2.ZIP успешно обработан: Автор='%s', Название='%s', Аннотация=%d символов",
						author, title, len(annotation))
				}
			}
		} else {
			fileType = "unknown"
		}
	default:
		fileType = "unknown"
	}

	if fileType == "unknown" {
		err = fmt.Errorf("неизвестный тип файла: %s", ext)
		return
	}

	// Логируем результаты
	if annotation != "" {
		if cfg.Debug {
			log.Printf("Извлечена аннотация для %s: %d символов", filePath, len(annotation))
		}
	}

	return
}

// extractInfoFromFilename извлекает автора и название из имени файла
// Используется как резервный способ, если метаданные не извлечены
// Для форматов DJVU/PDF предполагается, что если в имени есть " - " или " & ",
// то первая часть может содержать фамилию (или фамилии, разделенные пробелами)
func extractInfoFromFilename(filename string) (author, title string) {
	// Убираем расширение
	nameWithoutExt := strings.TrimSuffix(filename, filepath.Ext(filename))
	// Пробуем разделить по " - "
	parts := strings.SplitN(nameWithoutExt, " - ", 2)
	if len(parts) == 2 {
		author = strings.TrimSpace(parts[0])
		title = strings.TrimSpace(parts[1])
	} else {
		// Если нет " - ", считаем всё название книги
		title = strings.TrimSpace(nameWithoutExt)
		author = "Неизвестный автор"
	}
	// Если автор пустой (например, было " - Название"), установим значение по умолчанию
	if author == "" {
		author = "Неизвестный автор"
	}
	return author, title
}

// upsertAuthorsAndLink создает авторов (если они не существуют) и связывает их с книгой
// Эта функция остаётся почти без изменений, так как логика работы с авторами не меняется
func upsertAuthorsAndLink(bookID int, authorNamesStr string) error {
	// Разделяем строку авторов по запятым (если их несколько)
	authorNames := strings.Split(authorNamesStr, ",")
	var authors []Author
	for _, authorName := range authorNames {
		trimmedName := strings.TrimSpace(authorName)
		if trimmedName != "" {
			// Разбираем имя автора на части
			// Это резервный способ, если парсеры метаданных не предоставили FirstName/LastName
			// или если имя пришло из extractInfoFromFilename
			firstName := ""
			lastName := ""
			fullName := trimmedName // По умолчанию fullName - это всё имя
			// Простая эвристика: последнее слово - фамилия, остальное - имя
			// Это соответствует требованию для DJVU/PDF и служит запасным вариантом
			parts := strings.Fields(trimmedName)
			if len(parts) > 1 {
				lastName = parts[len(parts)-1]                      // Последнее слово
				firstName = strings.Join(parts[:len(parts)-1], " ") // Остальные слова
				// Формируем FullName без MiddleName, как указано в запросе
				if firstName != "" {
					fullName = firstName + " " + lastName
				} else {
					fullName = lastName
				}
			} else if len(parts) == 1 {
				lastName = parts[0]
				fullName = lastName
			}
			// Если parts пустой, firstName, lastName и fullName останутся пустыми
			authors = append(authors, Author{
				LastName: lastName,
				FullName: fullName, // Используем сформированное имя без MiddleName
			})
		}
	}
	// Если авторы не указаны, добавляем "Неизвестный автор"
	if len(authors) == 0 {
		authors = append(authors, Author{
			LastName: "Неизвестный",       // Для сортировки используем "Неизвестный"
			FullName: "Неизвестный автор", // Для отображения
		})
	}
	// Добавляем авторов и связи
	for _, author := range authors {
		// Проверяем, существует ли уже такой автор по FullName
		// (так как FullName теперь формируется без MiddleName, это должно быть более стабильно)
		var authorID int
		err := db.QueryRow("SELECT id FROM authors WHERE full_name = ?", author.FullName).Scan(&authorID)
		if err == sql.ErrNoRows {
			// Создаем нового автора
			_, err := db.Exec(
				"INSERT OR IGNORE INTO authors (last_name, full_name) VALUES (?, ?)",
				author.LastName, author.FullName,
			)
			if err != nil {
				fmt.Printf("Ошибка создания автора: %v\n", err)
				continue
			}
			// Получаем ID вставленного или существующего автора
			err = db.QueryRow("SELECT id FROM authors WHERE full_name = ?", author.FullName).Scan(&authorID)
			if err != nil {
				fmt.Printf("Ошибка получения ID автора: %v\n", err)
				continue
			}
		} else if err != nil {
			fmt.Printf("Ошибка проверки автора: %v\n", err)
			continue
		}
		// Связываем книгу с автором (если связь еще не существует)
		_, err = db.Exec("INSERT OR IGNORE INTO book_authors (book_id, author_id) VALUES (?, ?)", bookID, authorID)
		if err != nil {
			fmt.Printf("Ошибка связи книги с автором: %v\n", err)
			continue
		} else {
			fmt.Printf("  Автор: %s (Фамилия для сортировки: '%s')\n", author.FullName, author.LastName)
		}
	}
	return nil
}

// CleanupMissingFiles проверяет наличие файлов, записанных в БД.
// Если файлы отсутствуют, удаляет соответствующие записи из БД и файлы обложек.
func CleanupMissingFiles() error {
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}

	fmt.Println("Начинаю проверку на наличие файлов...")

	// 1. Получаем все записи из таблицы books с хешами файлов
	rows, err := db.Query(`
        SELECT id, file_url, file_hash 
        FROM books 
        WHERE file_hash IS NOT NULL AND file_hash != ''
    `)
	if err != nil {
		return fmt.Errorf("ошибка получения списка файлов из БД: %w", err)
	}
	defer rows.Close()

	// Счетчики для отчета
	var totalChecked int
	var deletedBooks int
	var booksToDelete []int
	var fileHashes []string // Собираем хеши для очистки обложек

	for rows.Next() {
		totalChecked++
		var bookID int
		var fileURL, fileHash sql.NullString

		err := rows.Scan(&bookID, &fileURL, &fileHash)
		if err != nil {
			fmt.Printf("Ошибка сканирования строки книги (ID: %d): %v\n", bookID, err)
			continue
		}

		// Проверяем, задан ли путь к файлу
		if !fileURL.Valid || fileURL.String == "" {
			fmt.Printf("Пропускаю книгу ID %d: путь к файлу не задан\n", bookID)
			continue
		}

		// В БД теперь хранятся абсолютные пути, используем их напрямую
		filePath := fileURL.String

		// Проверяем существование файла
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			fmt.Printf("Файл не найден: %s (Книга ID: %d). Планирую к удалению.\n", filePath, bookID)
			booksToDelete = append(booksToDelete, bookID)
		} else if err != nil {
			fmt.Printf("Ошибка проверки файла %s (Книга ID: %d): %v\n", filePath, bookID, err)
		}

		// Собираем хеши для очистки обложек
		if fileHash.Valid && fileHash.String != "" {
			fileHashes = append(fileHashes, fileHash.String)
		}
	}

	// Проверяем ошибки после итерации
	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}

	// 2. Удаляем записи из БД и файлы обложек, если есть что удалять
	if len(booksToDelete) > 0 {
		fmt.Printf("Найдено %d книг для удаления.\n", len(booksToDelete))

		// Начинаем транзакцию для обеспечения целостности данных
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("ошибка начала транзакции: %w", err)
		}
		// Отложенный откат транзакции в случае ошибки
		defer func() {
			if err != nil {
				_ = tx.Rollback()
				fmt.Println("Транзакция отменена из-за ошибки.")
			}
		}()

		// Создаем placeholder'ы для IN-запроса
		placeholders := make([]string, len(booksToDelete))
		args := make([]interface{}, len(booksToDelete))
		for i, id := range booksToDelete {
			placeholders[i] = "?"
			args[i] = id
		}
		placeholderStr := strings.Join(placeholders, ",")

		// Удаляем книги. CASCADE DELETE в схеме БД должен автоматически удалить
		// связанные записи из book_authors и book_tags.
		query := fmt.Sprintf("DELETE FROM books WHERE id IN (%s)", placeholderStr)
		_, err = tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("ошибка удаления книг из БД: %w", err)
		}
		deletedBooks = len(booksToDelete)

		// Фиксируем транзакцию
		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("ошибка фиксации транзакции: %w", err)
		}
		fmt.Printf("Удалено %d записей из БД.\n", deletedBooks)

		// 3. Удаляем файлы обложек
		cleanupUnusedCovers(fileHashes)

		// 4. Очищаем неиспользуемые данные (авторы, теги)
		//CleanupOrphanedData()

		return nil

	} else {
		fmt.Println("Все файлы из БД существуют на диске.")
	}

	fmt.Printf("Проверка завершена. Проверено: %d, Удалено записей: %d.\n", totalChecked, deletedBooks)
	return nil
}

// ProcessBookFile публичная функция для обработки файла книги
func ProcessBookFile(filePath string, info os.FileInfo) error {
	return processBookFile(filePath, info)
}

// saveAnnotationToFile сохраняет аннотацию в файл
func saveAnnotationToFile(bookID int, annotation, fileHash string) error {
	cfg := config.GetConfig()
	if annotation == "" {
		if cfg.Debug {
			log.Printf("Аннотация пуста для книги ID %d, файл не создается", bookID)
		}
		return nil
	}
	// ВСЕГДА определяем каталог notes относительно rootPath (каталога запуска)
	// игнорируя books_dir для веб-совместимости
	var notesDir string
	if rootPath != "" {
		notesDir = filepath.Join(rootPath, "notes")
	} else {
		notesDir = "./notes"
	}

	// Создаем каталог если его нет
	if _, err := os.Stat(notesDir); os.IsNotExist(err) {
		err = os.MkdirAll(notesDir, 0755)
		if err != nil {
			return fmt.Errorf("не удалось создать каталог notes: %w", err)
		}
	}

	// Формируем имя файла аннотации: {fileHash}.txt
	noteFileName := fileHash + ".txt"
	notePath := filepath.Join(notesDir, noteFileName)

	// Сохраняем аннотацию в файл
	err := os.WriteFile(notePath, []byte(annotation), 0644)
	if err != nil {
		return fmt.Errorf("не удалось сохранить аннотацию в файл %s: %w", notePath, err)
	}

	if cfg.Debug {
		log.Printf("Аннотация сохранена для книги ID %d: %s (%d символов)",
			bookID, notePath, len(annotation))
	}

	return nil
}

// CleanupMissingAnnotations удаляет файлы аннотаций, которые больше не связаны ни с одной книгой в БД
func CleanupMissingAnnotations() error {
	cfg := config.GetConfig() // Получаем конфиг для логов/дебага
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}

	// ВСЕГДА определяем каталог notes относительно rootPath (каталога запуска)
	// игнорируя books_dir для веб-совместимости и согласованности
	var notesDir string
	if rootPath != "" {
		notesDir = filepath.Join(rootPath, "notes")
	} else {
		notesDir = "./notes"
	}

	// Проверяем существование каталога
	if _, err := os.Stat(notesDir); os.IsNotExist(err) {
		if cfg.Debug {
			log.Println("Каталог notes не найден, очистка не требуется")
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("ошибка проверки каталога notes %s: %w", notesDir, err)
	}

	if cfg.Debug {
		log.Println("Начинаю проверку файлов аннотаций...")
	}

	// Получаем все хеши файлов из БД
	rows, err := db.Query("SELECT file_hash FROM books WHERE file_hash IS NOT NULL AND file_hash != ''")
	if err != nil {
		return fmt.Errorf("ошибка получения хешей из БД: %w", err)
	}
	defer rows.Close()

	// Создаем множество существующих хешей
	existingHashes := make(map[string]bool)
	for rows.Next() {
		var fileHash string
		if err := rows.Scan(&fileHash); err == nil {
			existingHashes[fileHash] = true
		}
	}
	// Проверяем ошибки после итерации по результатам запроса
	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}

	// Сканируем каталог notes
	files, err := os.ReadDir(notesDir)
	if err != nil {
		return fmt.Errorf("ошибка чтения каталога notes %s: %w", notesDir, err)
	}

	deletedFiles := 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Получаем имя файла без расширения
		fileName := file.Name()
		if filepath.Ext(fileName) == ".txt" {
			fileHash := strings.TrimSuffix(fileName, ".txt")

			// Если хеша нет в БД, удаляем файл
			if !existingHashes[fileHash] {
				filePath := filepath.Join(notesDir, file.Name())
				if err := os.Remove(filePath); err != nil {
					if cfg.Debug {
						log.Printf("Ошибка удаления файла аннотации %s: %v", filePath, err)
					}
				} else {
					if cfg.Debug {
						log.Printf("Удален файл аннотации: %s", filePath)
					}
					deletedFiles++
				}
			}
		}
	}

	if cfg.Debug {
		log.Printf("Очистка аннотаций завершена. Удалено файлов: %d", deletedFiles)
	}
	return nil
}

// cleanupUnusedCovers для очистки неиспользуемых обложек
func cleanupUnusedCovers(usedHashes []string) {
	coversDir := "./covers"
	if cfg != nil && cfg.BooksDir != "" {
		coversDir = filepath.Join(filepath.Dir(cfg.BooksDir), "covers")
	}

	// Проверяем существование каталога
	if _, err := os.Stat(coversDir); os.IsNotExist(err) {
		return
	}

	// Создаем множество используемых хешей
	usedHashesMap := make(map[string]bool)
	for _, hash := range usedHashes {
		usedHashesMap[hash] = true
	}

	// Сканируем каталог covers
	files, err := os.ReadDir(coversDir)
	if err != nil {
		fmt.Printf("Ошибка чтения каталога covers: %v\n", err)
		return
	}

	deletedCovers := 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Получаем имя файла без расширения
		fileName := file.Name()
		ext := filepath.Ext(fileName)
		fileHash := strings.TrimSuffix(fileName, ext)

		// Если хеша нет в используемых, удаляем файл
		if !usedHashesMap[fileHash] {
			filePath := filepath.Join(coversDir, file.Name())
			if err := os.Remove(filePath); err != nil {
				fmt.Printf("Ошибка удаления файла обложки %s: %v\n", filePath, err)
			} else {
				fmt.Printf("Удален файл обложки: %s\n", filePath)
				deletedCovers++
			}
		}
	}

	fmt.Printf("Удалено неиспользуемых обложек: %d\n", deletedCovers)
}

// CleanupOrphanedData очищает неиспользуемые авторы и теги из базы данных
func CleanupOrphanedData() error {
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}

	fmt.Println("Начинаю очистку неиспользуемых данных...")

	// Очищаем неиспользуемых авторов (которые не связаны ни с одной книгой)
	deletedAuthors, err := cleanupOrphanedAuthors()
	if err != nil {
		fmt.Printf("Ошибка очистки авторов: %v\n", err)
	} else {
		fmt.Printf("Удалено неиспользуемых авторов: %d\n", deletedAuthors)
	}

	// Очищаем неиспользуемые теги (которые не связаны ни с одной книгой)
	deletedTags, err := cleanupOrphanedTags()
	if err != nil {
		fmt.Printf("Ошибка очистки тегов: %v\n", err)
	} else {
		fmt.Printf("Удалено неиспользуемых тегов: %d\n", deletedTags)
	}

	// Примечание: серии не очищаются, так как они хранятся в таблице books
	// и автоматически становятся "непривязанными" при удалении книг.

	fmt.Println("Очистка неиспользуемых данных завершена.")
	return nil
}

// cleanupOrphanedAuthors удаляет авторов, которые не связаны ни с одной книгой
func cleanupOrphanedAuthors() (int64, error) {
	cfg := config.GetConfig()

	// Начинаем транзакцию
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer tx.Rollback()

	// Сначала удаляем "сиротские" записи в book_authors, которые ссылаются на несуществующие книги
	cleanupResult, err := tx.Exec(`
        DELETE FROM book_authors 
        WHERE book_id NOT IN (
            SELECT id FROM books
        )
    `)
	if err != nil {
		return 0, fmt.Errorf("ошибка очистки сиротских записей book_authors: %w", err)
	}

	cleanupCount, err := cleanupResult.RowsAffected()
	if err != nil {
		if cfg.Debug {
			log.Printf("Предупреждение: не удалось получить количество очищенных записей book_authors: %v", err)
		}
	} else if cleanupCount > 0 {
		if cfg.Debug {
			log.Printf("Очищено %d сиротских записей book_authors", cleanupCount)
		}
	}

	// Затем удаляем авторов, которые не связаны ни с одной книгой
	result, err := tx.Exec(`
        DELETE FROM authors 
        WHERE id NOT IN (
            SELECT DISTINCT author_id 
            FROM book_authors 
            WHERE author_id IS NOT NULL
        )
    `)
	if err != nil {
		return 0, fmt.Errorf("ошибка удаления неиспользуемых авторов: %w", err)
	}

	deletedCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("ошибка получения количества удаленных авторов: %w", err)
	}

	// Коммитим транзакцию
	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("ошибка коммита транзакции: %w", err)
	}

	if deletedCount > 0 {
		log.Printf("Удалено %d неиспользуемых авторов", deletedCount)
	}

	return deletedCount, nil
}

// cleanupOrphanedTags удаляет теги, которые не связаны ни с одной книгой
func cleanupOrphanedTags() (int64, error) {
	// Удаляем теги, у которых нет связей в таблице book_tags
	result, err := db.Exec(`
        DELETE FROM tags 
        WHERE id NOT IN (
            SELECT DISTINCT tag_id 
            FROM book_tags 
            WHERE tag_id IS NOT NULL
        )
    `)
	if err != nil {
		return 0, fmt.Errorf("ошибка удаления неиспользуемых тегов: %w", err)
	}

	deletedCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("ошибка получения количества удаленных тегов: %w", err)
	}

	return deletedCount, nil
}

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
	log.Printf("  Пропущено (нет файла или file_url/file_type): %d", skippedNoFileCount)
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
		if cfg.Debug {
			log.Println("Конфигурация не установлена, пропускаю переименование")
		}
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
			log.Printf("Книга ID %d переименована: %s -> %s", book.id, filePath, newPath)
		} else {
			skippedCount++
		}
	}

	log.Printf("Переименование завершено. Переименовано: %d, Пропущено: %d, Ошибок: %d",
		renamedCount, skippedCount, errorCount)
	return nil
}

// Вспомогательная функция для получения значения из sql.NullString
func getValueOrDefault(nullString sql.NullString) string {
	if nullString.Valid {
		return nullString.String
	}
	return ""
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

// GetConfig возвращает текущую конфигурацию приложения
func GetConfig() *config.Config {
	return cfg // Возвращает глобальную переменную cfg пакета scanner
}
