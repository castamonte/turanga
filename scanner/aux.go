// scanner/aux.go
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

// CleanupExtraFiles удаляет файлы из каталога books, которые не соответствуют ни одной записи в БД
func CleanupExtraFiles() error {
	cfg := config.GetConfig()
	if db == nil {
		return fmt.Errorf("база данных не инициализирована")
	}
	if cfg.Debug {
		log.Println("Начинаю проверку на лишние файлы в каталоге books...")
	}

	booksDir := cfg.GetBooksDirAbs(rootPath)
	if cfg.Debug {
		log.Printf("Каталог books: %s", booksDir)
	}

	// 1. Получаем все file_url из БД и сохраняем в map для быстрого поиска
	rows, err := db.Query("SELECT file_url FROM books WHERE file_url IS NOT NULL AND file_url != ''")
	if err != nil {
		return fmt.Errorf("ошибка получения списка файлов из БД: %w", err)
	}
	defer rows.Close()

	// Используем map[string]bool для хранения абсолютных путей из БД
	dbPaths := make(map[string]bool)
	for rows.Next() {
		var fileURL sql.NullString
		if err := rows.Scan(&fileURL); err != nil {
			if cfg.Debug {
				log.Printf("Ошибка сканирования строки файла из БД: %v", err)
			}
			continue
		}
		if fileURL.Valid && fileURL.String != "" {
			// Преобразуем путь из БД в абсолютный для корректного сравнения
			absPath, err := filepath.Abs(fileURL.String)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка получения абсолютного пути из БД для %s: %v", fileURL.String, err)
				}
				continue // Пропускаем некорректный путь
			}
			dbPaths[absPath] = true
		}
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("ошибка итерации по результатам запроса: %w", err)
	}

	// 2. Сканируем каталог books
	var extraFiles []string
	err = filepath.Walk(booksDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка доступа к файлу/каталогу %s: %v", path, err)
			}
			// Продолжаем сканирование
			return nil
		}
		if info.IsDir() {
			// Пропускаем подкаталоги? Или нужно проверять только сам booksDir?
			// Для безопасности проверим только сам booksDir.
			// Если нужно рекурсивно, убери этот if.
			// if path != booksDir {
			//     return filepath.SkipDir // Пропускаем подкаталоги
			// }
			return nil
		}

		// Преобразуем текущий путь к файлу в абсолютный
		absPath, err := filepath.Abs(path)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка получения абсолютного пути для %s: %v", path, err)
			}
			return nil // Пропускаем и продолжаем
		}

		// Проверяем, есть ли этот файл в БД
		if !dbPaths[absPath] {
			extraFiles = append(extraFiles, absPath)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("ошибка сканирования каталога books %s: %w", booksDir, err)
	}

	// 3. Удаляем лишние файлы
	deletedCount := 0
	for _, filePath := range extraFiles {
		// Дополнительная проверка: убедимся, что файл действительно в каталоге books
		relPath, err := filepath.Rel(booksDir, filePath)
		if err != nil || strings.HasPrefix(relPath, "..") {
			if cfg.Debug {
				log.Printf("Файл %s не находится в каталоге books %s, пропускаю для удаления.", filePath, booksDir)
			}
			continue
		}

		if cfg.Debug {
			log.Printf("Планирую удаление лишнего файла: %s", filePath)
		}
		err = os.Remove(filePath)
		if err != nil {
			if cfg.Debug {
				log.Printf("Ошибка удаления лишнего файла %s: %v", filePath, err)
			}
			// Продолжаем с другими файлами
		} else {
			deletedCount++
			if cfg.Debug {
				log.Printf("Удалён лишний файл: %s", filePath)
			}
		}
	}

	log.Printf("Проверка лишних файлов завершена. Найдено: %d, Удалено: %d", len(extraFiles), deletedCount)
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

// IsValidISBN проверяет, является ли строка действительным ISBN-10 или ISBN-13
func IsValidISBN(isbn string) bool {
	// Убираем все пробелы и тире
	cleanISBN := strings.ReplaceAll(strings.ReplaceAll(isbn, "-", ""), " ", "")

	// Проверяем длину
	if len(cleanISBN) != 10 && len(cleanISBN) != 13 {
		return false
	}

	switch len(cleanISBN) {
	case 10:
		return isValidISBN10(cleanISBN)
	case 13:
		return isValidISBN13(cleanISBN)
	default:
		return false
	}
}

// isValidISBN10 проверяет ISBN-10
func isValidISBN10(isbn string) bool {
	if len(isbn) != 10 {
		return false
	}

	sum := 0
	for i := 0; i < 9; i++ {
		digit := isbn[i] - '0'
		if digit < 0 || digit > 9 {
			return false
		}
		sum += int(digit) * (10 - i)
	}

	// Проверяем последнюю цифру (может быть 'X')
	lastChar := isbn[9]
	if lastChar == 'X' || lastChar == 'x' {
		sum += 10
	} else {
		digit := lastChar - '0'
		if digit < 0 || digit > 9 {
			return false
		}
		sum += int(digit)
	}

	return sum%11 == 0
}

// isValidISBN13 проверяет ISBN-13
func isValidISBN13(isbn string) bool {
	if len(isbn) != 13 {
		return false
	}

	sum := 0
	for i := 0; i < 12; i++ {
		digit := isbn[i] - '0'
		if digit < 0 || digit > 9 {
			return false
		}
		if i%2 == 0 {
			sum += int(digit)
		} else {
			sum += 3 * int(digit)
		}
	}

	checkDigit := isbn[12] - '0'
	if checkDigit < 0 || checkDigit > 9 {
		return false
	}

	return (sum+int(checkDigit))%10 == 0
}
