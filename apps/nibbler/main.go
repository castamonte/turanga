// cmd/nibbler/main.go
package main

import (
	"archive/zip"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"turanga/config"
	"turanga/scanner"

	"github.com/cespare/xxhash/v2"
	_ "github.com/mattn/go-sqlite3"
)

// Глобальный логгер для записи в файл
var fileLogger *log.Logger

// OperationMode определяет режим работы nibbler
type OperationMode string

const (
	ModeStay OperationMode = "stay" // Оставлять файлы на месте
	ModeCopy OperationMode = "copy" // Копировать файлы
	ModeMove OperationMode = "move" // Перемещать файлы
)

func main() {
	// Определяем путь к директории исполняемого файла
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Не удалось определить путь к исполняемому файлу: %v", err)
	}
	rootPath := filepath.Dir(exePath)
	log.Printf("Каталог приложения: %s", rootPath)

	// Настройка логирования в файл
	logFilePath := filepath.Join(rootPath, "nibbler.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Не удалось открыть файл лога %s: %v", logFilePath, err)
	} else {
		// Создаем MultiWriter, который пишет и в stdout, и в файл
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		// Устанавливаем стандартный логгер для использования MultiWriter
		log.SetOutput(multiWriter)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

		// Создаем отдельный логгер для файла (без дублирования в stdout)
		fileLogger = log.New(io.MultiWriter(logFile), "", log.Ldate|log.Ltime|log.Lmicroseconds)

		defer func() {
			if logFile != nil {
				logFile.Close()
			}
		}()
	}

	// Загружаем конфигурацию
	cfg, err := config.LoadConfig(filepath.Join(rootPath, "turanga.conf"))
	if err != nil {
		log.Printf("Ошибка загрузки конфигурации: %v", err)
		// Продолжаем с конфигом по умолчанию
		cfg = config.DefaultConfig()
	}

	// Валидируем конфигурацию
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Ошибка валидации конфигурации: %v", err)
	}

	config.SetGlobalConfig(cfg)

	// Инициализируем подключение к базе данных
	dbPath := filepath.Join(rootPath, "turanga.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Ошибка открытия БД %s: %v", dbPath, err)
	}
	defer db.Close()

	// Проверка соединения
	if err = db.Ping(); err != nil {
		log.Fatalf("Ошибка подключения к БД %s: %v", dbPath, err)
	}
	log.Printf("База данных успешно открыта: %s", dbPath)

	// Передаем DB и конфигурацию в scanner
	scanner.SetDB(db)
	scanner.SetConfig(cfg)
	scanner.SetRootPath(rootPath)

	// Обработка аргументов командной строки
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Использование: %s [параметры] <каталог_с_книгами>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Параметры:\n")
		fmt.Fprintf(os.Stderr, "  -stay    Оставлять файлы на месте (не копировать и не перемещать)\n")
		fmt.Fprintf(os.Stderr, "  -copy    Копировать файлы в каталог books (по умолчанию)\n")
		fmt.Fprintf(os.Stderr, "  -move    Перемещать файлы в каталог books\n")
		fmt.Fprintf(os.Stderr, "\nПримечание: можно указать только один из флагов -stay, -copy или -move\n")
	}

	var stayFlag = flag.Bool("stay", false, "Оставлять файлы на месте")
	var copyFlag = flag.Bool("copy", false, "Копировать файлы в каталог books")
	var moveFlag = flag.Bool("move", false, "Перемещать файлы в каталог books")

	flag.Parse()

	// Определяем режим работы
	mode := ModeCopy // по умолчанию
	flagsSet := 0
	if *stayFlag {
		mode = ModeStay
		flagsSet++
	}
	if *copyFlag {
		mode = ModeCopy
		flagsSet++
	}
	if *moveFlag {
		mode = ModeMove
		flagsSet++
	}

	// Проверяем, что установлен только один флаг
	if flagsSet > 1 {
		fmt.Fprintf(os.Stderr, "Ошибка: можно указать только один из флагов -stay, -copy или -move\n")
		flag.Usage()
		os.Exit(1)
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	sourceDir := flag.Arg(0)

	// Проверяем существование исходного каталога
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		log.Fatalf("Каталог %s не найден", sourceDir)
	}

	// Определяем целевой каталог из конфигурации
	targetDir := cfg.GetBooksDirAbs(rootPath)

	// Создаем целевой каталог, если он не существует (только если не stay режим)
	if mode != ModeStay {
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			log.Fatalf("Ошибка создания каталога %s: %v", targetDir, err)
		}
	}

	log.Printf("Сканирую каталог: %s", sourceDir)
	log.Printf("Каталог назначения: %s", targetDir)
	log.Printf("Режим работы: %s", mode)

	// Сканируем каталог и обрабатываем файлы
	if err := scanAndProcessBooks(sourceDir, targetDir, mode); err != nil {
		log.Fatalf("Ошибка при сканировании каталога: %v", err)
	}

	log.Println("Обработка завершена успешно")
}

// scanAndProcessBooks сканирует каталог с книгами и обрабатывает их
func scanAndProcessBooks(sourceDir, targetDir string, mode OperationMode) error {
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
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

		if isValidExt || (ext == ".zip" && isFB2Zip(info.Name())) {
			// Обрабатываем файл
			if err := processBookFile(path, targetDir, mode, info); err != nil {
				log.Printf("Ошибка обработки файла %s: %v", path, err)
			}
		}

		return nil
	})
}

// isFB2Zip проверяет, является ли файл FB2.ZIP
func isFB2Zip(filename string) bool {
	baseName := filename[:len(filename)-len(filepath.Ext(filename))]
	return strings.ToLower(filepath.Ext(baseName)) == ".fb2"
}

// processBookFile обрабатывает один файл книги
func processBookFile(sourcePath, targetDir string, mode OperationMode, info os.FileInfo) error {
	var filePath string  // Путь к файлу, который будет добавлен в БД
	var finalHash string // Хеш файла, который будет добавлен в БД

	// Определяем, является ли файл FB2 или FB2.ZIP
	ext := strings.ToLower(filepath.Ext(sourcePath))
	isFB2 := ext == ".fb2"
	isFB2Zip := ext == ".zip" && isFB2ZipFilename(sourcePath)

	// --- Определяем finalHash заранее ---
	if mode != ModeStay {
		if isFB2 {
			// 1. Создаем временный ZIP файл для вычисления хеша
			tempZipFile, err := os.CreateTemp("", "nibbler_hash_*.fb2.zip")
			if err != nil {
				return fmt.Errorf("ошибка создания временного файла для хеширования FB2: %w", err)
			}
			tempZipPathForHash := tempZipFile.Name()
			tempZipFile.Close()
			defer os.Remove(tempZipPathForHash) // Убедимся, что временный файл удален

			// 2. Упаковываем .fb2 во временный .zip для хеширования
			if err := zipFB2File(sourcePath, tempZipPathForHash); err != nil {
				return fmt.Errorf("ошибка упаковки FB2 файла для хеширования: %w", err)
			}

			// 3. Вычисляем хеш упакованного файла
			fileHash, err := calculateFileHash(tempZipPathForHash)
			if err != nil {
				return fmt.Errorf("ошибка вычисления хеша упакованного FB2 файла %s: %w", tempZipPathForHash, err)
			}
			finalHash = fileHash

		} else {
			// Для других типов файлов хеш считаем от оригинала
			fileHash, err := calculateFileHash(sourcePath)
			if err != nil {
				return fmt.Errorf("ошибка вычисления хеша файла %s: %w", sourcePath, err)
			}
			finalHash = fileHash
		}
	} else {
		// Если stay режим, хеш считаем от исходного файла
		var err error
		finalHash, err = calculateFileHash(sourcePath)
		if err != nil {
			return fmt.Errorf("ошибка вычисления хеша файла %s: %w", sourcePath, err)
		}
		// filePath будет sourcePath
		filePath = sourcePath
	}

	// --- Копируем обложку ДО вызова scanner.ProcessBookFile ---
	// Это позволяет scanner.ProcessBookFile пропустить извлечение, если обложка уже есть
	if mode != ModeStay && finalHash != "" {
		coverPath := filepath.Join(filepath.Dir(sourcePath), "cover.jpg")
		if _, err := os.Stat(coverPath); err == nil {
			coversDir := filepath.Join(targetDir, "..", "covers")
			if err := os.MkdirAll(coversDir, 0755); err != nil {
				log.Printf("Предупреждение: Не удалось создать каталог covers: %v", err)
			} else {
				targetCoverPath := filepath.Join(coversDir, finalHash+".jpg")
				// Проверяем, существует ли обложка уже
				if _, err := os.Stat(targetCoverPath); os.IsNotExist(err) {
					if err := copyFile(coverPath, targetCoverPath); err != nil {
						log.Printf("Предупреждение: Не удалось скопировать обложку %s: %v", coverPath, err)
					} else {
						log.Printf("Обложка скопирована: %s -> %s", coverPath, targetCoverPath)
					}
				} else {
					log.Printf("Обложка %s уже существует, пропущена", targetCoverPath)
				}
			}
		}
		// TODO: Аналогично для аннотации
	}

	// --- Основная логика обработки файла ---
	if mode != ModeStay {
		var tempTargetPath string // Путь к временному файлу перед финальным перемещением

		if isFB2 {
			// --- Обработка FB2 файла ---
			// 1. Создаем временный ZIP файл для финального файла
			tempZipFile, err := os.CreateTemp("", "nibbler_final_*.fb2.zip")
			if err != nil {
				return fmt.Errorf("ошибка создания временного файла для упаковки FB2: %w", err)
			}
			tempTargetPath = tempZipFile.Name()
			tempZipFile.Close()
			defer os.Remove(tempTargetPath) // Убедимся, что временный файл удален в случае ошибки до moveFile

			// 2. Упаковываем .fb2 в временный .zip
			if err := zipFB2File(sourcePath, tempTargetPath); err != nil {
				return fmt.Errorf("ошибка упаковки FB2 файла: %w", err)
			}

		} else {
			// --- Обработка других типов файлов ---
			// Для других файлов используем сам исходный файл как временный
			tempTargetPath = sourcePath
		}

		// 3. Используем уже вычисленный хеш (finalHash) для имени
		// ВСЕГДА используем правильное расширение для nibbler
		var targetFileName string
		if isFB2 {
			targetFileName = fmt.Sprintf("%s.fb2.zip", finalHash)
		} else if isFB2Zip {
			targetFileName = fmt.Sprintf("%s.fb2.zip", finalHash)
		} else {
			targetFileName = fmt.Sprintf("%s%s", finalHash, ext)
		}
		targetPath := filepath.Join(targetDir, targetFileName)

		// 4. Проверяем существование и обрабатываем в зависимости от режима
		if _, err := os.Stat(targetPath); err == nil {
			log.Printf("Файл %s уже существует, пропускаю %s (%s)", targetFileName, sourcePath, mode)
			filePath = targetPath // Продолжаем с существующим файлом
		} else if os.IsNotExist(err) {
			// Обрабатываем в зависимости от режима
			switch mode {
			case ModeCopy:
				// Копируем файл
				if isFB2 {
					// Для FB2 копируем временный zip файл
					if err := copyFile(tempTargetPath, targetPath); err != nil {
						return fmt.Errorf("ошибка копирования временного файла %s в %s: %w", tempTargetPath, targetPath, err)
					}
				} else {
					// Для других файлов копируем исходный файл
					if err := copyFile(tempTargetPath, targetPath); err != nil {
						return fmt.Errorf("ошибка копирования файла %s в %s: %w", tempTargetPath, targetPath, err)
					}
				}
				log.Printf("Файл скопирован: %s -> %s", sourcePath, targetPath)

			case ModeMove:
				// Перемещаем файл
				if isFB2 {
					// Для FB2 перемещаем временный zip файл
					if err := moveFile(tempTargetPath, targetPath); err != nil {
						return fmt.Errorf("ошибка перемещения временного файла %s в %s: %w", tempTargetPath, targetPath, err)
					}
				} else {
					// Для других файлов перемещаем исходный файл
					if err := moveFile(tempTargetPath, targetPath); err != nil {
						return fmt.Errorf("ошибка перемещения файла %s в %s: %w", tempTargetPath, targetPath, err)
					}
				}
				log.Printf("Файл перемещен: %s -> %s", sourcePath, targetPath)
			}

			filePath = targetPath
		} else {
			return fmt.Errorf("ошибка проверки существования файла %s: %w", targetPath, err)
		}
	} else {
		// ModeStay - используем исходный путь
		filePath = sourcePath
		log.Printf("Файл оставлен на месте: %s", sourcePath)
	}

	// --- ВАЖНО: Получаем абсолютный путь для передачи в scanner ---
	absoluteFilePath, err := filepath.Abs(filePath)
	if err != nil {
		// Если не удалось получить абсолютный путь, используем filePath как есть
		absoluteFilePath = filePath
		log.Printf("Предупреждение: Не удалось получить абсолютный путь для %s: %v", filePath, err)
	}

	// --- ВЫПОЛНЕНИЕ: Отключаем переименование в scanner ---
	// Получаем оригинальную настройку через функцию пакета scanner
	scannerCfg := scanner.GetConfig()
	originalRenameMode := ""
	if scannerCfg != nil {
		originalRenameMode = scannerCfg.RenameBook
		// Временно устанавливаем режим "no"
		scannerCfg.RenameBook = "no"
	}

	// --- Вызов scanner.ProcessBookFile ---
	fileInfo, err := os.Stat(absoluteFilePath)
	if err != nil {
		// --- ВОССТАНОВЛЕНИЕ: Восстанавливаем оригинальную настройку ---
		if scannerCfg != nil {
			scannerCfg.RenameBook = originalRenameMode
		}
		return fmt.Errorf("ошибка получения информации о файле %s: %w", absoluteFilePath, err)
	}

	err = scanner.ProcessBookFile(absoluteFilePath, fileInfo)

	// --- ВОССТАНОВЛЕНИЕ: Восстанавливаем оригинальную настройку ---
	if scannerCfg != nil {
		scannerCfg.RenameBook = originalRenameMode
	}

	if err != nil {
		return err
	}
	log.Printf("Успешно обработан файл: %s (Хеш: %s, Режим: %s)", absoluteFilePath, finalHash, mode)
	return nil
}

// isFB2ZipFilename проверяет, является ли файл FB2.ZIP по имени
func isFB2ZipFilename(filename string) bool {
	baseName := filename[:len(filename)-len(filepath.Ext(filename))]
	return strings.ToLower(filepath.Ext(baseName)) == ".fb2"
}

// moveFile перемещает файл из src в dst.
// Если os.Rename не работает из-за cross-device link, выполняет копирование и удаление исходного файла.
func moveFile(src, dst string) error {
	// Сначала пробуем os.Rename (быстрая операция на той же ФС)
	err := os.Rename(src, dst)
	if err == nil {
		// Успешно перемещено
		return nil
	}

	// Проверяем, является ли ошибка cross-device link
	// В Go 1.17+ можно использовать errors.Is(err, syscall.EXDEV)
	// Для более широкой совместимости проверяем строку ошибки
	if strings.Contains(err.Error(), "cross-device link") || strings.Contains(err.Error(), "invalid cross-device link") {
		// Ошибка cross-device, выполняем копирование и удаление
		if copyErr := copyFile(src, dst); copyErr != nil {
			return fmt.Errorf("ошибка копирования при обработке cross-device: %w (исходная ошибка: %v)", copyErr, err)
		}
		// После успешного копирования удаляем исходный файл
		if removeErr := os.Remove(src); removeErr != nil {
			// Логируем предупреждение, но не возвращаем ошибку,
			// так как файл уже успешно скопирован
			log.Printf("Предупреждение: не удалось удалить исходный файл %s после копирования: %v", src, removeErr)
		}
		return nil
	}

	// Другая ошибка при переименовании
	return fmt.Errorf("ошибка переименования %s в %s: %w", src, dst, err)
}

// copyFile копирует файл из src в dst
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
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

// zipFB2File упаковывает FB2 файл в ZIP архив
func zipFB2File(fb2Path, zipPath string) error {
	// Открываем исходный FB2 файл
	srcFile, err := os.Open(fb2Path)
	if err != nil {
		return fmt.Errorf("не удалось открыть FB2 файл %s: %w", fb2Path, err)
	}
	defer srcFile.Close()

	// Создаем ZIP файл
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("не удалось создать ZIP файл %s: %w", zipPath, err)
	}
	defer zipFile.Close()

	// Создаем ZIP writer
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Получаем имя файла без пути для использования внутри архива
	fb2FileName := filepath.Base(fb2Path)

	// Создаем файл внутри ZIP архива
	writer, err := zipWriter.Create(fb2FileName)
	if err != nil {
		return fmt.Errorf("не удалось создать запись в ZIP архиве: %w", err)
	}

	// Копируем содержимое FB2 файла в ZIP архив
	_, err = io.Copy(writer, srcFile)
	if err != nil {
		return fmt.Errorf("не удалось скопировать данные в ZIP архив: %w", err)
	}

	return nil
}
