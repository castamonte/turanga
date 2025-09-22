// web/ipfs.go
package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"turanga/config"
	"turanga/nostr"

	shell "github.com/ipfs/go-ipfs-api"
)

// DownloadIPFSBookHandler обрабатывает скачивание книги через IPFS
func (w *WebInterface) DownloadIPFSBookHandler(wr http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()

	// Проверяем аутентификацию
	if !w.isAuthenticated(r) {
		w.writeJSONError(wr, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Принимаем только POST запросы
	if r.Method != http.MethodPost {
		w.writeJSONError(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Парсим JSON данные
	var requestData struct {
		FileHash string `json:"file_hash"`
		IPFSCID  string `json:"ipfs_cid"`
		FileType string `json:"file_type"`
		Title    string `json:"title"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		w.writeJSONError(wr, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Валидация
	if requestData.FileHash == "" || requestData.IPFSCID == "" || requestData.FileType == "" {
		w.writeJSONError(wr, "Missing required parameters", http.StatusBadRequest)
		return
	}

	if cfg.Debug {
		log.Printf("Начинаем скачивание книги через IPFS: hash=%s, cid=%s, type=%s",
			requestData.FileHash, requestData.IPFSCID, requestData.FileType)
	}

	// Определяем расширение файла
	ext := getFileExtensionByType(requestData.FileType)
	if cfg.Debug {
		log.Printf("Определено расширение: '%s' для типа файла: '%s'", ext, requestData.FileType)
	}
	if ext == "" {
		ext = ".bin"
		if cfg.Debug {
			log.Printf("Используем расширение по умолчанию .bin")
		}
	}

	// Определяем каталог books
	booksDir := "./books" // значение по умолчанию

	// Получаем путь к исполняемому файлу для определения корневой директории
	execPath, err := os.Executable()
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка получения пути к исполняемому файлу: %v", err)
		}
	} else {
		execDir := filepath.Dir(execPath)

		if w.config != nil && w.config.BooksDir != "" {
			// Если BooksDir абсолютный путь - используем его
			if filepath.IsAbs(w.config.BooksDir) {
				booksDir = w.config.BooksDir
			} else {
				// Если относительный - строим путь относительно директории программы
				booksDir = filepath.Join(execDir, w.config.BooksDir)
			}
		} else {
			// Если конфиг не задан или BooksDir пуст - используем books в директории программы
			booksDir = filepath.Join(execDir, "books")
		}
	}

	// Создаем каталог если он не существует
	if err := os.MkdirAll(booksDir, 0755); err != nil {
		log.Printf("Ошибка создания каталога books: %v", err)
		w.writeJSONError(wr, "Failed to create books directory", http.StatusInternalServerError)
		return
	}

	if cfg.Debug {
		log.Printf("Каталог для сохранения книг: %s", booksDir)
	}

	// Формируем путь к файлу
	fileName := fmt.Sprintf("%s%s", requestData.FileHash, ext)
	filePath := filepath.Join(booksDir, fileName)

	// Проверяем, не существует ли уже файл
	if _, err := os.Stat(filePath); err == nil {
		if cfg.Debug {
			log.Printf("Файл уже существует: %s", filePath)
		}
		w.writeJSONResponse(wr, map[string]interface{}{
			"success":  true,
			"message":  "File already exists",
			"path":     filePath,
			"fileHash": requestData.FileHash,
		})
		return
	}

	// Получаем IPFS клиент
	ipfsShell, err := w.getIPFSShell()
	if err != nil {
		log.Printf("Ошибка получения IPFS клиента: %v", err)
		w.writeJSONError(wr, "IPFS not configured", http.StatusInternalServerError)
		return
	}

	// Скачиваем файл
	err = w.downloadIPFSFile(ipfsShell, requestData.IPFSCID, filePath)
	if err != nil {
		log.Printf("Ошибка скачивания файла из IPFS: %v", err)
		w.writeJSONError(wr, "Failed to download file from IPFS: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Файл успешно скачан: %s", filePath)

	// Получаем информацию о файле
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		log.Printf("Ошибка получения информации о файле: %v", err)
		w.writeJSONResponse(wr, map[string]interface{}{
			"success":  false,
			"message":  "File downloaded but failed to get file info: " + err.Error(),
			"path":     filePath,
			"fileHash": requestData.FileHash,
		})
		return
	}

	// Регистрируем файл в БД через scanner
	err = w.registerDownloadedBook(filePath, fileInfo, requestData.Title, requestData.FileHash)
	if err != nil {
		if cfg.Debug {
			log.Printf("Ошибка регистрации книги в БД: %v", err)
		}
		// Не возвращаем ошибку, так как файл уже скачан
		w.writeJSONResponse(wr, map[string]interface{}{
			"success":  false,
			"message":  "File downloaded but failed to register in DB: " + err.Error(),
			"path":     filePath,
			"fileHash": requestData.FileHash,
		})
		return
	}

	if cfg.Debug {
		log.Printf("Книга успешно зарегистрирована в БД: %s", filePath)
	}

	// Получаем ID зарегистрированной книги из БД и pubkey отправителя
	var bookID int64
	var responderPubkey sql.NullString

	// Ищем pubkey отправителя через file_hash в nostr_response_books и nostr_received_responses
	// Выполняем запрос асинхронно чтобы не блокировать основной поток
	go func() {
		err := w.db.QueryRow(`
			SELECT b.id, r.responder_pubkey 
			FROM books b
			LEFT JOIN nostr_response_books rb ON b.file_hash = rb.file_hash
			LEFT JOIN nostr_received_responses r ON rb.response_id = r.id
			WHERE b.file_hash = ? 
			LIMIT 1
		`, requestData.FileHash).Scan(&bookID, &responderPubkey)

		if err != nil && err != sql.ErrNoRows {
			if cfg.Debug {
				log.Printf("Ошибка получения ID книги и pubkey отправителя из БД: %v", err)
			}
			return
		}

		// Если найден pubkey отправителя, увеличиваем счетчик бонусов
		if responderPubkey.Valid && responderPubkey.String != "" && w.NostrClient != nil {
			// Создаем временный SubscriptionManager для вызова функции
			subManager := nostr.NewSubscriptionManager(w.NostrClient, w.config, w.db)
			err := subManager.IncrementFriendDownloadCount(responderPubkey.String)
			if err != nil {
				if cfg.Debug {
					log.Printf("Ошибка увеличения счетчика бонусов для друга %s: %v", responderPubkey.String, err)
				}
			} else {
				if cfg.Debug {
					log.Printf("Увеличен счетчик бонусов для друга %s при скачивании книги %s",
						responderPubkey.String, requestData.FileHash)
				}
			}
		}
	}()

	// Отправляем успешный ответ с информацией о скачанной книге (немедленно)
	w.writeJSONResponse(wr, map[string]interface{}{
		"success":  true,
		"message":  "File downloaded and registered successfully",
		"path":     filePath,
		"fileHash": requestData.FileHash,
		"book_id":  bookID,
	})
}

// Вспомогательная функция для получения IPFS shell
func (w *WebInterface) getIPFSShell() (*shell.Shell, error) {
	if w.config == nil {
		return nil, fmt.Errorf("config not available")
	}
	return w.config.GetIPFSShell()
}

// Вспомогательная функция для скачивания файла из IPFS
func (w *WebInterface) downloadIPFSFile(ipfsShell *shell.Shell, cid, filePath string) error {
	// Скачиваем файл из IPFS в указанный путь
	err := ipfsShell.Get(cid, filePath)
	if err != nil {
		// Удаляем файл в случае ошибки
		os.Remove(filePath)
		return fmt.Errorf("failed to download from IPFS: %w", err)
	}

	return nil
}

// addFileToIPFS добавляет файл в IPFS и возвращает CID
func (w *WebInterface) addFileToIPFS(filePath string, fileInfo os.FileInfo) (string, error) {
	cfg := config.GetConfig()

	if cfg.Debug {
		log.Printf("addFileToIPFS: Начало для файла %s", filePath)
	}

	// Проверяем конфигурацию
	if w.config == nil {
		if cfg.Debug {
			log.Println("addFileToIPFS: Конфигурация не установлена")
		}
		return "", fmt.Errorf("конфигурация не установлена")
	}

	// Получаем IPFS shell
	if cfg.Debug {
		log.Println("addFileToIPFS: Получаю IPFS shell...")
	}
	ipfsShell, err := w.config.GetIPFSShell()
	if err != nil {
		log.Printf("addFileToIPFS: Не удалось получить IPFS shell: %v", err)
		return "", fmt.Errorf("не удалось получить IPFS shell: %w", err)
	}
	if cfg.Debug {
		log.Println("addFileToIPFS: IPFS shell успешно получен")
	}

	// Открываем файл для чтения
	if cfg.Debug {
		log.Printf("addFileToIPFS: Открываю файл %s для чтения...", filePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		if cfg.Debug {
			log.Printf("addFileToIPFS: Не удалось открыть файл: %v", err)
		}
		return "", fmt.Errorf("не удалось открыть файл: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			if cfg.Debug {
				log.Printf("addFileToIPFS: Ошибка закрытия файла %s: %v", filePath, closeErr)
			}
		} else {
			if cfg.Debug {
				log.Printf("addFileToIPFS: Файл %s успешно закрыт", filePath)
			}
		}
	}()
	if cfg.Debug {
		log.Printf("addFileToIPFS: Файл %s открыт", filePath)
	}

	// Добавляем файл в IPFS
	if cfg.Debug {
		log.Printf("addFileToIPFS: Начинаю добавление файла в IPFS...")
	}
	cid, err := ipfsShell.Add(file, shell.Pin(true))
	if err != nil {
		log.Printf("addFileToIPFS: Не удалось добавить файл в IPFS: %v", err)
		return "", fmt.Errorf("не удалось добавить файл в IPFS: %w", err)
	}
	if cfg.Debug {
		log.Printf("addFileToIPFS: Файл успешно добавлен в IPFS. CID: %s", cid)
	}

	return cid, nil
}

// isIPFSAvailable проверяет, доступен ли IPFS
func (w *WebInterface) isIPFSAvailable() bool {
	cfg := config.GetConfig()

	// Проверяем, инициализирована ли конфигурация
	if w.config == nil {
		log.Println("IPFS config is nil")
		return false
	}

	// Проверяем, задан ли адрес API в конфиге
	if w.config.LocalIPFSAPI == "" {
		if cfg.Debug {
			log.Println("IPFS API address is not set in config")
		}
		return false
	}

	// Пытаемся получить клиент IPFS shell
	ipfsShell, err := w.getIPFSShell()
	if err != nil {
		if cfg.Debug {
			log.Printf("Failed to get IPFS shell: %v", err)
		}
		return false
	}

	// Выполняем простую проверку доступности, например, получение версии
	// Version() возвращает (string, string, error)
	version, _, err := ipfsShell.Version()
	if err != nil {
		if cfg.Debug {
			log.Printf("IPFS is not reachable or returned an error on version check: %v", err)
		}
		return false
	}

	if cfg.Debug {
		log.Printf("IPFS is available. Version: %s", version)
	}
	return true
}
