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
				if cfg.Debug {
					log.Printf("Файл уже в БД (пропущен): %s\n", path)
				}
				return nil
			}

			// Файл новый, обрабатываем его
			if cfg.Debug {
				log.Printf("Обрабатываю новый файл: %s\n", path)
			}
			processErr := processBookFile(path, info)
			if processErr != nil {
				fmt.Printf("Ошибка обработки файла %s: %v\n", path, processErr)
				errorCount++
			} else {
				addedCount++
				if cfg.Debug {
					log.Printf("Файл добавлен в БД: %s\n", path)
				}
				// Обработка завершена внутри processBookFile:
				// - вставка в БД
				// - извлечение и сохранение обложки
				// - извлечение и сохранение аннотации
				// - добавление в IPFS
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
func processBookFile(filePath string, info os.FileInfo) error {
	// Проверка хеша файла
	fileHash, err := calculateFileHash(filePath)
	if err != nil {
		if cfg.Debug {
			log.Printf("⚠️ Не удалось вычислить хеш для файла %s: %v. Пропускаю.", filePath, err)
		}
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
		if cfg.Debug {
			log.Printf("Файл %s (хеш: %s%s) уже существует в БД, ID %d (URL: %s). Пропускаю.", filePath, fileHash, cidInfo, existingBookID, existingFileURL.String)
		}
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
		if cfg.Debug {
			log.Printf("Не удалось определить тип файла для %s: %v", filePath, err)
		}
		return nil
	}
	// Если не удалось извлечь метаданные или нет названия, используем имя файла
	if title == "" {
		authorName, title = extractInfoFromFilename(info.Name())
		if cfg.Debug {
			log.Printf("Использую данные из имени файла для %s: Автор='%s', Название='%s'", filePath, authorName, title)
		}
	}
	// Проверка, что у нас есть хотя бы название
	if title == "" {
		if cfg.Debug {
			log.Printf("⚠️ Не удалось определить название для %s", filePath)
		}
		return nil // Не ошибка, просто пропускаем файл
	}
	// Переименовываем файл, если это указано в конфигурации
	// Делаем это до вычисления относительного пути, чтобы путь был актуальным
	filePath, err = renameBookFile(originalFilePath, authorName, title, fileType, fileHash)
	if err != nil {
		if cfg.Debug {
			log.Printf("⚠️ ошибка переименования файла %s: %v", originalFilePath, err)
		}
		// Продолжаем обработку с оригинальным путем
		filePath = originalFilePath
	}
	// Получаем абсолютный путь к файлу для хранения в БД
	absoluteFilePath, err := filepath.Abs(filePath)
	if err != nil {
		// Если не удалось получить абсолютный путь, используем filePath как есть
		absoluteFilePath = filePath
		if cfg.Debug {
			log.Printf("⚠️ Не удалось получить абсолютный путь для %s: %v", filePath, err)
		}
	}
	// Извлекаем относительный путь (уже с новым именем файла, если переименование произошло)
	var relPath string
	if rootPath != "" {
		// Если задан rootPath, формируем относительный путь от него
		relPath, err = filepath.Rel(rootPath, filePath)
		if err != nil {
			return fmt.Errorf("⚠️ не удалось получить относительный путь от %s к %s: %w", rootPath, filePath, err)
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
	// Добавляем lower-поля
	result, err := db.Exec(
		`INSERT INTO books 
		(title, series, series_number, published_at, isbn, year, publisher, file_url, file_type, file_hash, file_size, over18, ipfs_cid, title_lower, series_lower) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		title, series, seriesNumber, publishedAt, isbn, year, publisher, absoluteFilePath, fileType, fileHash, fileSize, false, nil, // false = не 18+, ipfs_cid = nil -> NULL в БД
		strings.ToLower(title), strings.ToLower(series), // для lower-полей
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
				if cfg.Debug {
					log.Printf("⚠️ Не удалось открыть файл %s для добавления в IPFS: %v.", filePath, openErr)
				}
			} else {
				// КРИТИЧЕСКОЕ ИЗМЕНЕНИЕ: Закрываем файл НЕМЕДЛЕННО после использования
				func() {
					defer fileForIPFS.Close() // Закрываем сразу после Add
					// Добавляем файл в IPFS с --nocopy и --pin
					cid, addErr := ipfsShell.Add(fileForIPFS, ipfsapi.Pin(true))
					if addErr != nil {
						// Не критично для основного процесса, логируем предупреждение
						if cfg.Debug {
							log.Printf("⚠️ Не удалось добавить файл %s в IPFS: %v.", filePath, addErr)
						}
						// Можно рассмотреть возврат ошибки, если IPFS критичен
						// return fmt.Errorf("критическая ошибка добавления в IPFS: %w", addErr)
					} else {
						ipfsCID = cid
						if cfg.Debug {
							log.Printf("Файл %s успешно добавлен в IPFS с CID: %s", filePath, ipfsCID)
						}
						// Обновляем запись в БД с полученным CID
						_, updateErr := db.Exec("UPDATE books SET ipfs_cid = ? WHERE id = ?", ipfsCID, bookID)
						if updateErr != nil {
							if cfg.Debug {
								log.Printf("⚠️ Не удалось сохранить IPFS CID в БД для книги ID %d: %v.", bookID, updateErr)
							}
							// Не возвращаем ошибку, чтобы не прерывать основной процесс
						}
					}
				}() // Немедленный вызов для закрытия файла
			}
		}
	} else {
		if cfg.Debug {
			log.Println("⚠️ Конфигурация не установлена. Продолжаю без добавления в IPFS.")
		}
	}
	// После успешной вставки книги сохраняем аннотацию в файл:
	err = saveAnnotationToFile(bookID, annotation, fileHash)
	if err != nil {
		if cfg.Debug {
			log.Printf("⚠️ ошибка сохранения аннотации для книги %d: %v", bookID, err)
		}
		// Не прерываем процесс из-за ошибки аннотации
	}
	if cfg.Debug {
		log.Printf("Добавлена новая книга: %s (ID: %d)", title, bookID)
		log.Printf("  ISBN: %s, Год: %s, Издатель: %s, Серия: %s", isbn, year, publisher, series)
		log.Printf("  Файл: %s (%s), хеш: %s", relPath, fileType, fileHash)
		if ipfsCID != "" {
			log.Printf("  IPFS CID: %s", ipfsCID)
		}
	}
	// Обрабатываем авторов
	err = upsertAuthorsAndLink(bookID, authorName)
	if err != nil {
		if cfg.Debug {
			log.Printf("⚠️ ошибка обработки авторов для %s: %v", filePath, err)
		}
		// Не прерываем процесс из-за ошибки авторов
	}
	// Извлекаем обложку
	err = extractAndSaveCover(filePath, fileType, bookID, fileHash)
	if err != nil {
		if cfg.Debug {
			log.Printf("⚠️ не удалось извлечь обложку для %s: %v", filePath, err)
		}
		// Не прерываем процесс из-за ошибки обложки
	}
	log.Printf("✅ %s (Название: '%s', Автор: '%s', Хеш: %s)", filePath, title, authorName, fileHash)
	return nil
}

// extractAndSaveCover извлекает и сохраняет обложку книги
func extractAndSaveCover(filePath, fileType string, bookID int, fileHash string) error {
	// Вызываем ExtractCover, передавая fileHash
	coverURL, err := ExtractCover(filePath, fileType, bookID, fileHash)
	if err == nil && coverURL != "" {
		// Не сохраняем coverURL в БД, просто логируем
		if cfg.Debug {
			log.Printf("Добавлена обложка для книги %d: %s\n", bookID, filepath.Base(coverURL))
		}
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
		author, title, err = ExtractDJVUMetadata(filePath, cfg)
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
// Использует full_name_lower и last_name_lower для поиска и создания авторов.
func upsertAuthorsAndLink(bookID int, authorNamesStr string) error {
	cfg := config.GetConfig() // Получаем конфиг для логов/дебага

	// Разделяем строку авторов по запятым (если их несколько)
	authorNames := strings.Split(authorNamesStr, ",")
	var authors []Author

	for _, authorName := range authorNames {
		trimmedName := strings.TrimSpace(authorName)
		if trimmedName != "" {
			// Очищаем имя от лишних пробелов
			cleanedName := strings.Join(strings.Fields(trimmedName), " ")

			if cleanedName != "" {
				// Вычисляем last_name_lower как последнее слово в cleanedName, приведенное к нижнему регистру
				var lastNameLower string
				if parts := strings.Fields(cleanedName); len(parts) > 0 {
					lastNameLower = strings.ToLower(parts[len(parts)-1])
				} else {
					lastNameLower = strings.ToLower(cleanedName)
				}

				// Формируем full_name_lower - ни к чему, надёжнее вычислить заново
				//fullNameLower := strings.ToLower(cleanedName)

				// Добавляем в список авторов для обработки
				// Используем LastName для хранения lastNameLower (для совместимости с моделью Author,
				// хотя поле будет содержать значение из last_name_lower БД)
				// FullName хранит оригинальное очищенное имя
				authors = append(authors, Author{
					LastName: lastNameLower, // Храним lastNameLower в поле LastName для использования в сортировке/поиске
					FullName: cleanedName,   // Храним оригинальное очищенное имя для отображения и поиска по full_name
				})
			}
		}
	}

	// Если авторы не указаны, добавляем "Неизвестный автор"
	if len(authors) == 0 {
		// Для "Неизвестный автор" last_name_lower будет "неизвестный"
		authors = append(authors, Author{
			LastName: "неизвестный",       // last_name_lower для сортировки
			FullName: "Неизвестный автор", // Для отображения
		})
	}

	// Добавляем авторов и связи
	for _, author := range authors {
		// Проверяем, существует ли уже такой автор по FullName (регистрозависимо, как раньше)
		// или по full_name_lower (регистронезависимо, новая логика)
		// Сначала пробуем найти по full_name (старая логика для совместимости)
		var authorID int
		err := db.QueryRow("SELECT id FROM authors WHERE full_name = ?", author.FullName).Scan(&authorID)

		if err == sql.ErrNoRows {
			// Не найден по full_name, пробуем найти по full_name_lower (новая логика)
			err = db.QueryRow("SELECT id FROM authors WHERE full_name_lower = ?", strings.ToLower(author.FullName)).Scan(&authorID)

			if err == sql.ErrNoRows {
				// Автор не найден ни по full_name, ни по full_name_lower - создаем нового
				// Вычисляем last_name_lower для нового автора
				var lastNameLower string
				if parts := strings.Fields(author.FullName); len(parts) > 0 {
					lastNameLower = strings.ToLower(parts[len(parts)-1])
				} else {
					lastNameLower = strings.ToLower(author.FullName)
				}

				_, err := db.Exec(
					"INSERT OR IGNORE INTO authors (last_name_lower, full_name, full_name_lower) VALUES (?, ?, ?)",
					lastNameLower, author.FullName, strings.ToLower(author.FullName),
				)
				if err != nil {
					if cfg.Debug {
						log.Printf("Ошибка создания автора '%s': %v", author.FullName, err)
					}
					continue
				}

				// Получаем ID вставленного автора по full_name_lower
				err = db.QueryRow("SELECT id FROM authors WHERE full_name_lower = ?", strings.ToLower(author.FullName)).Scan(&authorID)
				if err != nil {
					if cfg.Debug {
						log.Printf("Ошибка получения ID автора '%s' после вставки: %v", author.FullName, err)
					}
					continue
				}

				if cfg.Debug {
					log.Printf("Создан новый автор: %s (ID: %d)", author.FullName, authorID)
				}

			} else if err != nil {
				// Другая ошибка при поиске по full_name_lower
				if cfg.Debug {
					log.Printf("Ошибка проверки автора по full_name_lower '%s': %v", author.FullName, err)
				}
				continue
			} else {
				// Найден по full_name_lower
				if cfg.Debug {
					log.Printf("Найден существующий автор по full_name_lower '%s' (ID: %d)", author.FullName, authorID)
				}
			}
		} else if err != nil {
			// Другая ошибка при поиске по full_name
			if cfg.Debug {
				log.Printf("Ошибка проверки автора по full_name '%s': %v", author.FullName, err)
			}
			continue
		} else {
			// Найден по full_name
			if cfg.Debug {
				log.Printf("Найден существующий автор по full_name '%s' (ID: %d)", author.FullName, authorID)
			}
		}

		// Связываем книгу с автором (если связь еще не существует)
		_, err = db.Exec("INSERT OR IGNORE INTO book_authors (book_id, author_id) VALUES (?, ?)", bookID, authorID)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка связи книги %d с автором %d (%s): %v", bookID, authorID, author.FullName, err)
			}
			continue
		} else {
			if cfg.Debug {
				log.Printf("  Автор связан: %s (Фамилия для сортировки: '%s')", author.FullName, author.LastName)
			}
		}
	}

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

// GetConfig возвращает текущую конфигурацию приложения
func GetConfig() *config.Config {
	return cfg // Возвращает глобальную переменную cfg пакета scanner
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

// Вспомогательная функция для получения значения из sql.NullString
func getValueOrDefault(nullString sql.NullString) string {
	if nullString.Valid {
		return nullString.String
	}
	return ""
}

// FillMissingLowercaseFields заполняет недостающие lower-поля для книг и авторов.
// Используется во время ревизии для обработки записей, добавленных до миграции.
func FillMissingLowercaseFields() error {
	cfg := config.GetConfig()
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}

	if cfg.Debug {
		log.Println("FillMissingLowercaseFields: Начало заполнения недостающих lower-полей...")
	}

	// --- Обновляем книги ---
	if cfg.Debug {
		log.Println("FillMissingLowercaseFields: Обновляю недостающие lower-поля для книг...")
	}

	const bookBatchSize = 1000
	updatedBooks := 0
	totalBatches := 0

	for {
		totalBatches++

		// Начинаем транзакцию для пакета
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("ошибка начала транзакции для обновления книг (пакет %d): %w", totalBatches, err)
		}
		rollbackTx := func() {
			if rbErr := tx.Rollback(); rbErr != nil {
				if cfg.Debug {
					log.Printf("FillMissingLowercaseFields: Ошибка отката транзакции для книг (пакет %d): %v", totalBatches, rbErr)
				}
			} else if cfg.Debug {
				log.Printf("FillMissingLowercaseFields: Транзакция для книг (пакет %d) отменена", totalBatches)
			}
		}

		// Подготавливаем запрос обновления
		stmt, err := tx.Prepare("UPDATE books SET title_lower = ?, series_lower = ? WHERE id = ? AND (title_lower IS NULL OR title_lower = '')")
		if err != nil {
			rollbackTx()
			return fmt.Errorf("ошибка подготовки запроса обновления книг (пакет %d): %w", totalBatches, err)
		}
		defer stmt.Close()

		// Получаем пакет записей, где title_lower NULL или пустой
		rows, err := tx.Query(`
			SELECT id, title, series 
			FROM books 
			WHERE title_lower IS NULL OR title_lower = '' 
			LIMIT ?`, bookBatchSize)
		if err != nil {
			rollbackTx()
			return fmt.Errorf("ошибка запроса книг для обновления (пакет %d): %w", totalBatches, err)
		}

		idsToUpdate := make([]int, 0, bookBatchSize)
		titlesToUpdate := make([]sql.NullString, 0, bookBatchSize)
		seriesToUpdate := make([]sql.NullString, 0, bookBatchSize)

		for rows.Next() {
			var id int
			var title, series sql.NullString
			if scanErr := rows.Scan(&id, &title, &series); scanErr != nil {
				if cfg.Debug {
					log.Printf("FillMissingLowercaseFields: Ошибка сканирования строки книги (ID: %d): %v", id, scanErr)
				}
				continue // Пропускаем проблемную строку, но продолжаем
			}
			idsToUpdate = append(idsToUpdate, id)
			titlesToUpdate = append(titlesToUpdate, title)
			seriesToUpdate = append(seriesToUpdate, series)
		}
		rows.Close()

		if len(idsToUpdate) == 0 {
			// Нет больше записей для обновления
			_ = tx.Rollback() // Откатываем пустую транзакцию
			if cfg.Debug {
				log.Println("FillMissingLowercaseFields: Больше нет книг с пустыми title_lower для обновления.")
			}
			break
		}

		// Выполняем обновления
		batchUpdated := 0
		for i, id := range idsToUpdate {
			titleLower := ""
			seriesLower := ""

			if titlesToUpdate[i].Valid && titlesToUpdate[i].String != "" {
				titleLower = strings.ToLower(titlesToUpdate[i].String)
			}
			if seriesToUpdate[i].Valid && seriesToUpdate[i].String != "" {
				seriesLower = strings.ToLower(seriesToUpdate[i].String)
			}

			_, execErr := stmt.Exec(titleLower, seriesLower, id)
			if execErr != nil {
				if cfg.Debug {
					log.Printf("FillMissingLowercaseFields: Ошибка обновления книги ID %d: %v", id, execErr)
				}
				// Продолжаем с другими записями в пакете
			} else {
				batchUpdated++
			}
		}

		// Завершаем транзакцию
		err = tx.Commit()
		if err != nil {
			rollbackTx()
			return fmt.Errorf("ошибка коммита транзакции для обновления книг (пакет %d): %w", totalBatches, err)
		}

		updatedBooks += batchUpdated
		if cfg.Debug {
			log.Printf("FillMissingLowercaseFields: Пакет %d завершён. Обновлено книг: %d", totalBatches, batchUpdated)
		}

		// Если обработано меньше, чем размер пакета, значит, это был последний пакет
		if len(idsToUpdate) < bookBatchSize {
			break
		}
	}

	if cfg.Debug {
		log.Printf("FillMissingLowercaseFields: Завершено обновление книг. Всего обновлено: %d", updatedBooks)
	}

	// --- Обновляем авторов ---
	if cfg.Debug {
		log.Println("FillMissingLowercaseFields: Обновляю недостающие lower-поля для авторов...")
	}

	updatedAuthors := 0
	totalAuthorBatches := 0

	for {
		totalAuthorBatches++

		// Начинаем транзакцию для пакета
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("ошибка начала транзакции для обновления авторов (пакет %d): %w", totalAuthorBatches, err)
		}
		rollbackTx := func() {
			if rbErr := tx.Rollback(); rbErr != nil {
				if cfg.Debug {
					log.Printf("FillMissingLowercaseFields: Ошибка отката транзакции для авторов (пакет %d): %v", totalAuthorBatches, rbErr)
				}
			} else if cfg.Debug {
				log.Printf("FillMissingLowercaseFields: Транзакция для авторов (пакет %d) отменена", totalAuthorBatches)
			}
		}

		// Подготавливаем запрос обновления
		stmt, err := tx.Prepare("UPDATE authors SET last_name_lower = ?, full_name_lower = ? WHERE id = ? AND (last_name_lower IS NULL OR last_name_lower = '' OR full_name_lower IS NULL OR full_name_lower = '')")
		if err != nil {
			rollbackTx()
			return fmt.Errorf("ошибка подготовки запроса обновления авторов (пакет %d): %w", totalAuthorBatches, err)
		}
		defer stmt.Close()

		// Получаем пакет записей, где last_name_lower/full_name_lower NULL или пустые
		rows, err := tx.Query(`
			SELECT id, last_name_lower, full_name 
			FROM authors 
			WHERE last_name_lower IS NULL OR last_name_lower = '' OR full_name_lower IS NULL OR full_name_lower = '' 
			LIMIT ?`, bookBatchSize)
		if err != nil {
			rollbackTx()
			return fmt.Errorf("ошибка запроса авторов для обновления (пакет %d): %w", totalAuthorBatches, err)
		}

		idsToUpdate := make([]int, 0, bookBatchSize)
		lastNamesToUpdate := make([]sql.NullString, 0, bookBatchSize)
		fullNamesToUpdate := make([]sql.NullString, 0, bookBatchSize)

		for rows.Next() {
			var id int
			var lastName, fullName sql.NullString
			if scanErr := rows.Scan(&id, &lastName, &fullName); scanErr != nil {
				if cfg.Debug {
					log.Printf("FillMissingLowercaseFields: Ошибка сканирования строки автора (ID: %d): %v", id, scanErr)
				}
				continue // Пропускаем проблемную строку, но продолжаем
			}
			idsToUpdate = append(idsToUpdate, id)
			lastNamesToUpdate = append(lastNamesToUpdate, lastName)
			fullNamesToUpdate = append(fullNamesToUpdate, fullName)
		}
		rows.Close()

		if len(idsToUpdate) == 0 {
			// Нет больше записей для обновления
			_ = tx.Rollback() // Откатываем пустую транзакцию
			if cfg.Debug {
				log.Println("FillMissingLowercaseFields: Больше нет авторов с пустыми lower-полями для обновления.")
			}
			break
		}

		// Выполняем обновления
		batchUpdated := 0
		for i, id := range idsToUpdate {
			lastNameLower := ""
			fullNameLower := ""

			if lastNamesToUpdate[i].Valid && lastNamesToUpdate[i].String != "" {
				lastNameLower = strings.ToLower(lastNamesToUpdate[i].String)
			}
			if fullNamesToUpdate[i].Valid && fullNamesToUpdate[i].String != "" {
				fullNameLower = strings.ToLower(fullNamesToUpdate[i].String)
			}

			_, execErr := stmt.Exec(lastNameLower, fullNameLower, id)
			if execErr != nil {
				if cfg.Debug {
					log.Printf("FillMissingLowercaseFields: Ошибка обновления автора ID %d: %v", id, execErr)
				}
				// Продолжаем с другими записями в пакете
			} else {
				batchUpdated++
			}
		}

		// Завершаем транзакцию
		err = tx.Commit()
		if err != nil {
			rollbackTx()
			return fmt.Errorf("ошибка коммита транзакции для обновления авторов (пакет %d): %w", totalAuthorBatches, err)
		}

		updatedAuthors += batchUpdated
		if cfg.Debug {
			log.Printf("FillMissingLowercaseFields: Пакет авторов %d завершён. Обновлено авторов: %d", totalAuthorBatches, batchUpdated)
		}

		// Если обработано меньше, чем размер пакета, значит, это был последний пакет
		if len(idsToUpdate) < bookBatchSize {
			break
		}
	}

	if cfg.Debug {
		log.Printf("FillMissingLowercaseFields: Завершено обновление авторов. Всего обновлено: %d", updatedAuthors)
		log.Println("FillMissingLowercaseFields: Завершено заполнение недостающих lower-полей.")
	}

	return nil
}
