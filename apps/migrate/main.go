// apps/migrate/main.go

package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Глобальная переменная для подключения к БД
var db *sql.DB

func main() {
	// Определяем путь к директории исполняемого файла
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Не удалось определить путь к исполняемому файлу: %v", err)
	}
	rootPath := filepath.Dir(exePath)
	log.Printf("ℹ️ Каталог приложения: %s", rootPath)

	// Настройка логирования в файл
	logFilePath := filepath.Join(rootPath, "migrate.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Не удалось открыть файл лога %s: %v", logFilePath, err)
		// Продолжаем, даже если не удалось открыть файл лога
	} else {
		// Создаем MultiWriter, который пишет и в stdout, и в файл
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		// Устанавливаем стандартный логгер для использования MultiWriter
		log.SetOutput(multiWriter)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

		defer func() {
			if logFile != nil {
				logFile.Close()
			}
		}()
	}

	// Инициализируем подключение к базе данных
	dbPath := filepath.Join(rootPath, "turanga.db")
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Ошибка открытия БД %s: %v", dbPath, err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()

	// Проверка соединения
	if err = db.Ping(); err != nil {
		log.Fatalf("Ошибка подключения к БД %s: %v", dbPath, err)
	}
	log.Printf("ℹ️ База данных успешно открыта: %s", dbPath)

	// Выполняем миграцию
	if err := MigrateToLowercaseFields(dbPath); err != nil {
		log.Fatalf("Ошибка миграции: %v", err)
	}

	log.Println("ℹ️ Миграция успешно завершена.")
}

// updateBooksLowerFields обновляет *_lower поля для таблицы books.
// Использует пакетную обработку и транзакции.
func updateBooksLowerFields() error {
	const batchSize = 1000

	var totalRecords int
	err := db.QueryRow("SELECT COUNT(*) FROM books WHERE title_lower IS NULL OR series_lower IS NULL").Scan(&totalRecords)
	if err != nil {
		return fmt.Errorf("ошибка получения количества записей книг: %w", err)
	}

	if totalRecords == 0 {
		log.Println("ℹ️ Все книги уже имеют заполненные *_lower поля, пропускаю миграцию книг.")
		return nil
	}

	log.Printf("ℹ️ Найдено %d записей книг для миграции в *_lower поля.", totalRecords)

	offset := 0
	updatedBooks := 0

	for {
		// Получаем пачку записей
		rows, err := db.Query(`
			SELECT id, title, series 
			FROM books 
			WHERE title_lower IS NULL OR series_lower IS NULL 
			LIMIT ? OFFSET ?`, batchSize, offset)
		if err != nil {
			return fmt.Errorf("ошибка запроса книг: %w", err)
		}

		recordsToUpdate := make([]struct {
			id     int
			title  sql.NullString
			series sql.NullString
		}, 0, batchSize)

		for rows.Next() {
			var record struct {
				id     int
				title  sql.NullString
				series sql.NullString
			}
			if err := rows.Scan(&record.id, &record.title, &record.series); err != nil {
				log.Printf("Ошибка сканирования строки книги: %v", err)
				continue
			}
			recordsToUpdate = append(recordsToUpdate, record)
		}
		rows.Close()

		if len(recordsToUpdate) == 0 {
			break
		}

		// Обновляем в транзакции
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("ошибка начала транзакции: %w", err)
		}
		defer tx.Rollback()

		stmt, err := tx.Prepare("UPDATE books SET title_lower = ?, series_lower = ? WHERE id = ?")
		if err != nil {
			return fmt.Errorf("ошибка подготовки запроса обновления книг: %w", err)
		}
		defer stmt.Close()

		for _, record := range recordsToUpdate {
			titleLower := ""
			seriesLower := ""
			if record.title.Valid {
				titleLower = strings.ToLower(record.title.String)
			}
			if record.series.Valid {
				seriesLower = strings.ToLower(record.series.String)
			}
			_, err := stmt.Exec(titleLower, seriesLower, record.id)
			if err != nil {
				log.Printf("Ошибка обновления *_lower для книги ID %d: %v", record.id, err)
			} else {
				updatedBooks++
			}
		}

		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("ошибка коммита транзакции обновления книг: %w", err)
		}

		log.Printf("ℹ️ Обновлено %d из %d записей книг.", updatedBooks, totalRecords)

		if len(recordsToUpdate) < batchSize {
			break
		}
		offset += batchSize
	}

	log.Printf("ℹ️ Обновлено %d записей книг.", updatedBooks)
	return nil
}

// updateAuthorsLowerFields обновляет *_lower поля для таблицы authors.
func updateAuthorsLowerFields() error {
	const batchSize = 1000

	// Сначала проверим, есть ли вообще что обновлять
	var totalRecords int
	err := db.QueryRow("SELECT COUNT(*) FROM authors WHERE last_name_lower IS NULL OR full_name_lower IS NULL").Scan(&totalRecords)
	if err != nil {
		return fmt.Errorf("ошибка получения количества записей авторов: %w", err)
	}

	if totalRecords == 0 {
		log.Println("ℹ️ Все авторы уже имеют заполненные *_lower поля, пропускаю миграцию авторов.")
		return nil
	}

	log.Printf("Найдено %d записей авторов для миграции в *_lower поля.", totalRecords)

	offset := 0
	updatedAuthors := 0

	for {
		// Получаем пачку записей
		// ВАЖНО: Запрашиваем только id, full_name. НЕ запрашиваем last_name или last_name_lower из WHERE.
		rows, err := db.Query(`
			SELECT id, full_name 
			FROM authors 
			WHERE last_name_lower IS NULL OR full_name_lower IS NULL 
			LIMIT ? OFFSET ?`, batchSize, offset)
		if err != nil {
			return fmt.Errorf("ошибка запроса авторов: %w", err)
		}

		recordsToUpdate := make([]struct {
			id       int
			fullName sql.NullString
		}, 0, batchSize)

		for rows.Next() {
			var record struct {
				id       int
				fullName sql.NullString
			}
			if err := rows.Scan(&record.id, &record.fullName); err != nil {
				log.Printf("Ошибка сканирования строки автора: %v", err)
				continue
			}
			recordsToUpdate = append(recordsToUpdate, record)
		}
		rows.Close()

		if len(recordsToUpdate) == 0 {
			break
		}

		// Обновляем в транзакции
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("ошибка начала транзакции: %w", err)
		}
		defer tx.Rollback()

		// Подготавливаем запрос обновления. Он НЕ должен ссылаться на last_name в SET.
		stmt, err := tx.Prepare("UPDATE authors SET last_name_lower = ?, full_name_lower = ? WHERE id = ?")
		if err != nil {
			return fmt.Errorf("ошибка подготовки запроса обновления авторов: %w", err)
		}
		defer stmt.Close()

		for _, record := range recordsToUpdate {
			lastNameLower := ""
			fullNameLower := ""

			if record.fullName.Valid {
				fullNameLower = strings.ToLower(record.fullName.String)
				// Вычисляем last_name_lower как последнее слово из full_name
				if record.fullName.String != "" {
					parts := strings.Fields(record.fullName.String)
					if len(parts) > 0 {
						lastNameLower = strings.ToLower(parts[len(parts)-1])
					} else {
						lastNameLower = fullNameLower
					}
				}
			}

			_, err := stmt.Exec(lastNameLower, fullNameLower, record.id)
			if err != nil {
				log.Printf("Ошибка обновления *_lower для автора ID %d: %v", record.id, err)
			} else {
				updatedAuthors++
			}
		}

		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("ошибка коммита транзакции обновления авторов: %w", err)
		}

		log.Printf("ℹ️ Обновлено %d из %d записей авторов.", updatedAuthors, totalRecords)

		if len(recordsToUpdate) < batchSize {
			break
		}
		offset += batchSize
	}

	log.Printf("ℹ️ Обновлено %d записей авторов.", updatedAuthors)
	return nil
}

// columnExists проверяет, существует ли колонка в таблице.
func columnExists(database *sql.DB, tableName, columnName string) (bool, error) {
	query := fmt.Sprintf("SELECT %s FROM %s LIMIT 1", columnName, tableName)
	var dummy interface{}
	err := database.QueryRow(query).Scan(&dummy) // ВАЖНО: &dummy
	if err != nil {
		if strings.Contains(err.Error(), "no such column") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// backupDatabase создает резервную копию файла базы данных.
// Возвращает путь к резервной копии или ошибку.
func backupDatabase(dbPath string) (string, error) {
	backupPath := dbPath + ".bak." + time.Now().Format("20060102_150405")
	// Используем "cp" для создания резервной копии на уровне файловой системы.
	// Это быстрее и безопаснее, чем копирование средствами Go, особенно для больших БД.
	// Убедитесь, что команда "cp" доступна в вашей системе.
	cmd := exec.Command("cp", dbPath, backupPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ошибка создания резервной копии '%s' -> '%s': %w, вывод: %s", dbPath, backupPath, err, output)
	}
	log.Printf("ℹ️ Резервная копия БД создана: %s", backupPath)
	return backupPath, nil
}

// MigrateToLowercaseFields выполняет полную миграцию:
// 1. Создает резервную копию БД.
// 2. Удаляет устаревший столбец last_name из таблицы authors.
// 3. Добавляет недостающие *_lower столбцы.
// 4. Заполняет *_lower столбцы данными.
// 5. Создает индексы на *_lower столбцы.
func MigrateToLowercaseFields(dbPath string) error {
	log.Println("Начинаю миграцию в lower-поля...")

	// --- БЛОК 0: Резервное копирование ---
	if dbPath != "" {
		_, err := backupDatabase(dbPath)
		if err != nil {
			// Можно сделать возврат с ошибкой, если резервное копирование критично
			// return fmt.Errorf("ошибка создания резервной копии БД: %w", err)
			// Или просто предупредить и продолжить
			log.Printf("Предупреждение: не удалось создать резервную копию БД: %v. Продолжаю миграцию.", err)
		}
		// Если dbPath пустой, логируем предупреждение
	} else {
		log.Println("Предупреждение: Путь к БД не указан, пропускаю создание резервной копии.")
	}
	// --- КОНЕЦ БЛОКА 0 ---

	log.Println("Проверяю существование lower-полей...")

	// --- БЛОК 1: Удаление устаревшего столбца last_name из таблицы authors ---
	// Проверяем, существует ли устаревший столбец last_name в таблице authors
	hasOldLastName, err := columnExists(db, "authors", "last_name")
	if err != nil {
		// Это не критично, если проверка не удалась, просто логируем
		log.Printf("Предупреждение: ошибка проверки существования устаревшего столбца 'last_name': %v", err)
		hasOldLastName = false // Предполагаем, что его нет или мы не можем проверить
	}

	if hasOldLastName {
		log.Println("Обнаружен устаревший столбец 'last_name' в таблице authors. Планирую удаление...")
		// SQLite не поддерживает прямое удаление столбцов через ALTER TABLE DROP COLUMN
		// до версии 3.35.0. Для совместимости используем подход с пересозданием таблицы.

		// НАЧАЛО: Универсальный подход - пересоздание таблицы
		log.Println("Начинаю удаление устаревшего столбца 'last_name' из таблицы authors (пересоздание таблицы)...")

		// 1. Переименовываем оригинальную таблицу
		_, err = db.Exec("ALTER TABLE authors RENAME TO authors_old")
		if err != nil {
			log.Printf("Ошибка переименования таблицы authors: %v. Пропускаю удаление last_name.", err)
			// Пытаемся восстановить исходное имя таблицы на случай, если RENAME частично прошло
			_, _ = db.Exec("ALTER TABLE authors_old RENAME TO authors") // Пытаемся восстановить
		} else {
			// 2. Создаем новую таблицу БЕЗ столбца last_name
			_, err = db.Exec(`
			CREATE TABLE authors (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				full_name TEXT UNIQUE NOT NULL
				-- *_lower столбцы добавятся ниже, если их нет
			)`)
			if err != nil {
				log.Printf("Ошибка создания новой таблицы authors: %v. Откатываю изменения.", err)
				_, _ = db.Exec("DROP TABLE IF EXISTS authors")
				_, _ = db.Exec("ALTER TABLE authors_old RENAME TO authors")
			} else {
				// 3. Копируем ТОЛЬКО существующие данные (id, full_name)
				//    Не пытаемся копировать last_name или *_lower, так как их может не быть
				_, err = db.Exec(`
					INSERT INTO authors (id, full_name)
					SELECT id, full_name
					FROM authors_old
				`)
				if err != nil {
					log.Printf("Ошибка копирования данных в новую таблицу authors: %v. Откатываю изменения.", err)
					_, _ = db.Exec("DROP TABLE authors")
					_, _ = db.Exec("ALTER TABLE authors_old RENAME TO authors")
				} else {
					log.Println("Данные успешно скопированы в новую таблицу authors без столбца 'last_name'.")
					// 4. Удаляем старую таблицу
					_, err = db.Exec("DROP TABLE authors_old")
					if err != nil {
						log.Printf("Предупреждение: не удалось удалить временную таблицу authors_old: %v", err)
						// Это не критично, данные уже перенесены
					} else {
						log.Println("Устаревший столбец 'last_name' успешно удален из таблицы authors.")
					}
				}
			}
		}
		// КОНЕЦ: Универсальный подход
	} else {
		log.Println("Устаревший столбец 'last_name' в таблице authors не найден. Пропускаю удаление.")
	}
	// --- КОНЕЦ БЛОКА 1 ---

	// --- БЛОК 2: Добавление *_lower столбцов (если они еще не существуют) ---
	hasTitleLower, err := columnExists(db, "books", "title_lower")
	if err != nil {
		return fmt.Errorf("ошибка проверки существования колонки title_lower: %w", err)
	}
	hasSeriesLower, err := columnExists(db, "books", "series_lower")
	if err != nil {
		return fmt.Errorf("ошибка проверки существования колонки series_lower: %w", err)
	}
	hasLastNameLower, err := columnExists(db, "authors", "last_name_lower")
	if err != nil {
		return fmt.Errorf("ошибка проверки существования колонки last_name_lower: %w", err)
	}
	hasFullNameLower, err := columnExists(db, "authors", "full_name_lower")
	if err != nil {
		return fmt.Errorf("ошибка проверки существования колонки full_name_lower: %w", err)
	}

	if !hasTitleLower {
		log.Println("Добавляю колонку title_lower в таблицу books...")
		_, err = db.Exec("ALTER TABLE books ADD COLUMN title_lower TEXT")
		if err != nil {
			// Проверим, не потому ли ошибка, что колонка уже существует
			if checkIfExists, checkErr := columnExists(db, "books", "title_lower"); checkErr == nil && checkIfExists {
				log.Println("Колонка title_lower уже существует.")
			} else {
				return fmt.Errorf("ошибка добавления колонки title_lower: %w", err)
			}
		}
	}
	if !hasSeriesLower {
		log.Println("Добавляю колонку series_lower в таблицу books...")
		_, err = db.Exec("ALTER TABLE books ADD COLUMN series_lower TEXT")
		if err != nil {
			// Проверим, не потому ли ошибка, что колонка уже существует
			if checkIfExists, checkErr := columnExists(db, "books", "series_lower"); checkErr == nil && checkIfExists {
				log.Println("Колонка series_lower уже существует.")
			} else {
				return fmt.Errorf("ошибка добавления колонки series_lower: %w", err)
			}
		}
	}
	if !hasLastNameLower {
		log.Println("Добавляю колонку last_name_lower в таблицу authors...")
		_, err = db.Exec("ALTER TABLE authors ADD COLUMN last_name_lower TEXT")
		if err != nil {
			// Проверим, не потому ли ошибка, что колонка уже существует
			if checkIfExists, checkErr := columnExists(db, "authors", "last_name_lower"); checkErr == nil && checkIfExists {
				log.Println("Колонка last_name_lower уже существует.")
			} else {
				return fmt.Errorf("ошибка добавления колонки last_name_lower: %w", err)
			}
		}
	}
	if !hasFullNameLower {
		log.Println("Добавляю колонку full_name_lower в таблицу authors...")
		_, err = db.Exec("ALTER TABLE authors ADD COLUMN full_name_lower TEXT")
		if err != nil {
			// Проверим, не потому ли ошибка, что колонка уже существует
			if checkIfExists, checkErr := columnExists(db, "authors", "full_name_lower"); checkErr == nil && checkIfExists {
				log.Println("Колонка full_name_lower уже существует.")
			} else {
				return fmt.Errorf("ошибка добавления колонки full_name_lower: %w", err)
			}
		}
	}
	// --- КОНЕЦ БЛОКА 2 ---

	log.Println("Начинаю заполнение lower-полей...")

	// --- БЛОК 3: Заполнение *_lower полей ---
	// Заполняем lower-поля для книг
	err = updateBooksLowerFields()
	if err != nil {
		return fmt.Errorf("ошибка миграции lower-полей для книг: %w", err)
	}

	// Заполняем lower-поля для авторов (ИСПРАВЛЕННАЯ ЛОГИКА)
	err = updateAuthorsLowerFields()
	if err != nil {
		return fmt.Errorf("ошибка миграции lower-полей для авторов: %w", err)
	}
	// --- КОНЕЦ БЛОКА 3 ---

	// --- БЛОК 4: Создание индексов ---
	log.Println("Создаю индексы для lower-полей...")
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_books_title_lower ON books(title_lower)")
	if err != nil {
		log.Printf("Предупреждение: ошибка создания индекса idx_books_title_lower: %v", err)
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_books_series_lower ON books(series_lower)")
	if err != nil {
		log.Printf("Предупреждение: ошибка создания индекса idx_books_series_lower: %v", err)
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_authors_last_name_lower ON authors(last_name_lower)")
	if err != nil {
		log.Printf("Предупреждение: ошибка создания индекса idx_authors_last_name_lower: %v", err)
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_authors_full_name_lower ON authors(full_name_lower)")
	if err != nil {
		log.Printf("Предупреждение: ошибка создания индекса idx_authors_full_name_lower: %v", err)
	}
	// --- КОНЕЦ БЛОКА 4 ---

	log.Println("ℹ️ Миграция в lower-поля завершена")
	return nil
}
